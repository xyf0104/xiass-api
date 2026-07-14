#!/usr/bin/env python3
import importlib.machinery
import importlib.util
import io
import json
import tempfile
import unittest
from pathlib import Path
from unittest import mock


ROOT = Path(__file__).resolve().parents[1]
AGENT_PATH = ROOT / "files/usr/bin/xiass-soft-router-agent"
LOADER = importlib.machinery.SourceFileLoader("xiass_soft_router_agent", str(AGENT_PATH))
SPEC = importlib.util.spec_from_loader(LOADER.name, LOADER)
agent = importlib.util.module_from_spec(SPEC)
LOADER.exec_module(agent)


class AgentTest(unittest.TestCase):
    def setUp(self):
        agent.RUNTIME_SECRETS.clear()

    def test_frp_names_are_canonicalized(self):
        self.assertEqual(agent.safe_frp_name("nowind-12-Japan"), "xiass-12-Japan")
        self.assertEqual(agent.safe_frp_name("东京 节点"), "xiass-socks")
        self.assertEqual(agent.safe_frp_name("XIASS.alpha_1"), "XIASS.alpha_1")

    def test_old_desired_json_aliases_remain_supported(self):
        desired = {
            "nowind_enabled": "1",
            "frp_host": "frps.example.com",
            "frp_port": 7010,
            "token": "frp-test-secret",
            "frp_mappings": [
                {
                    "active": True,
                    "local_port": 1081,
                    "remote_port": 12083,
                    "proxy_name": "nowind-japan",
                }
            ],
        }
        with mock.patch.object(agent, "uci_get", return_value="127.0.0.1"):
            rendered = agent.render_frpc_config(desired)
        self.assertIn("[xiass-japan]", rendered)
        self.assertIn("server_addr = frps.example.com", rendered)
        self.assertIn("remote_port = 12083", rendered)
        self.assertNotIn("[nowind-", rendered)

    def test_report_adds_xiass_identity_without_changing_server_fields(self):
        nodes = [{"id": "extra.1081", "openwrt_port": 1081}]
        with mock.patch.object(agent, "collect_socks_nodes", return_value=(nodes, True)):
            report = agent.build_report()
        self.assertEqual(report["socks"], nodes)
        self.assertTrue(report["snapshot_complete"])
        self.assertEqual(report["xiass_agent"]["name"], "xiass-soft-router-agent")
        self.assertEqual(report["xiass_agent"]["schema_version"], 2)

    def test_status_redaction_removes_secret_keys_and_values(self):
        secret = "agent-secret-value"
        agent.remember_secret(secret)
        status = agent.sanitize_status_data(
            {
                "message": "Authorization: Bearer %s" % secret,
                "agent_token": secret,
                "nested": {"last_error": "token=%s" % secret},
            }
        )
        encoded = json.dumps(status, ensure_ascii=False)
        self.assertNotIn(secret, encoded)
        self.assertNotIn("agent_token", status)
        self.assertIn("***", encoded)

    def test_legacy_uci_package_and_field_alias_are_read(self):
        values = {
            (agent.LEGACY_CONFIG_NAME, "api_url"): "https://legacy.example.com",
            (agent.LEGACY_CONFIG_NAME, "enabled"): "1",
        }

        def package_get(package, option, default=""):
            return values.get((package, option), default)

        with mock.patch.object(agent, "config_package_order", return_value=[agent.LEGACY_CONFIG_NAME, agent.CONFIG_NAME]), mock.patch.object(
            agent, "package_get", side_effect=package_get
        ):
            self.assertEqual(agent.uci_get("api_url"), "https://legacy.example.com")
            self.assertTrue(agent.cfg_bool("enabled"))

    def test_hk_frpc_pid_is_never_killed(self):
        spec = (Path("/tmp/agent.pid"), Path("/etc/frp/xiass-soft-router-frpc.ini"))
        with mock.patch.object(agent, "all_owned_frpc_specs", return_value=[spec]), mock.patch.object(
            agent, "read_pid", return_value=4321
        ), mock.patch.object(agent, "pid_running", return_value=True), mock.patch.object(
            agent, "process_cmdline", return_value=["/usr/bin/frpc", "-c", "/etc/frp/hk-frpc.ini"]
        ), mock.patch.object(agent, "log"), mock.patch.object(agent.os, "kill") as kill:
            self.assertFalse(agent.stop_frpc(all_owned=True))
            kill.assert_not_called()

    def test_agent_owned_legacy_frpc_is_recognized(self):
        args = ["/usr/bin/frpc", "-c", "/etc/frp/nowind-soft-router-frpc.ini"]
        self.assertTrue(agent.cmdline_owns_frpc(args, [agent.LEGACY_FRPC_CONFIG]))
        self.assertFalse(agent.cmdline_owns_frpc(args, [Path("/etc/frp/hk-frpc.ini")]))

    def test_frpc_status_is_green_when_no_mapping_is_required(self):
        with mock.patch.object(
            agent,
            "frpc_process_state",
            return_value={"pid": 0, "running": False, "owned": False, "message": "frpc 未运行"},
        ):
            status = agent.compute_frpc_status({"required": False})
        self.assertTrue(status["ok"])
        self.assertTrue(status["process_ok"])
        self.assertTrue(status["control_ok"])

    def test_frpc_control_failure_is_reported_separately(self):
        process = {"pid": 99, "running": True, "owned": True, "message": "frpc 正在运行"}
        existing = {
            "required": True,
            "control_host": "frps.example.com",
            "control_port": 7010,
            "proxy_count": 1,
            "login_state": "unknown",
        }
        with mock.patch.object(agent, "frpc_process_state", return_value=process), mock.patch.object(
            agent, "tcp_endpoint_ok", return_value=(False, "FRP 控制端口不可达")
        ):
            status = agent.compute_frpc_status(existing)
        self.assertTrue(status["process_ok"])
        self.assertFalse(status["control_ok"])
        self.assertFalse(status["ok"])

    def test_unknown_frpc_login_or_mapping_state_is_not_green(self):
        process = {"pid": 77, "running": True, "owned": True, "message": "frpc 正在运行"}
        existing = {
            "required": True,
            "control_host": "frps.example.com",
            "control_port": 7010,
            "proxy_count": 1,
            "login_state": "unknown",
            "tunnel_state": "unknown",
        }
        with mock.patch.object(agent, "frpc_process_state", return_value=process), mock.patch.object(
            agent, "tcp_endpoint_ok", return_value=(True, "FRP 控制端口可达")
        ):
            status = agent.compute_frpc_status(existing)
        self.assertFalse(status["control_ok"])
        self.assertFalse(status["ok"])
        self.assertIn("等待", status["control_message"])

    def test_tunnel_and_passwall_exit_are_independent(self):
        process = {"pid": 99, "running": True, "owned": True, "message": "frpc 正在运行"}
        frpc = {
            "required": True,
            "control_host": "api.example.com",
            "control_port": 7010,
            "proxy_count": 6,
            "login_state": "success",
            "tunnel_state": "success",
            "latest_log": "start proxy success",
        }
        report = {
            "socks": [
                {
                    "name": "SS-%d" % idx,
                    "openwrt_port": 1080 + idx,
                    "exit_status": "unreachable",
                    "exit_message": "外部上游不可达",
                }
                for idx in range(6)
            ]
        }
        with mock.patch.object(agent, "frpc_process_state", return_value=process), mock.patch.object(
            agent, "tcp_endpoint_ok", return_value=(True, "FRP 控制端口可达")
        ):
            tunnel = agent.compute_frpc_status(frpc)
        exit_ok, exit_message, details = agent.exit_status_from_report(report)
        self.assertTrue(tunnel["ok"])
        self.assertFalse(exit_ok)
        self.assertEqual(details["unreachable"], 6)
        self.assertIn("0 个可达", exit_message)

    def test_disabled_socks_exit_is_skipped_from_health_denominator(self):
        report = {
            "socks": [
                {"name": "enabled", "openwrt_port": 1080, "exit_status": "reachable"},
                {"name": "disabled", "openwrt_port": 1081, "exit_status": "skipped"},
            ]
        }
        ok, message, details = agent.exit_status_from_report(report)
        self.assertTrue(ok)
        self.assertEqual(details["eligible"], 1)
        self.assertEqual(details["skipped"], 1)
        self.assertIn("全部可达", message)

    def test_disabled_socks_is_not_probed(self):
        nodes = [{"name": "disabled", "openwrt_port": 1080, "enabled": False, "listen_status": "listening"}]
        with mock.patch.object(agent, "exit_probe_target", return_value=("api.example.com", 443, 5, "https", "/")), mock.patch.object(
            agent, "socks5_connect_ok"
        ) as probe:
            result = agent.attach_exit_probes(nodes)
        self.assertEqual(result[0]["exit_status"], "skipped")
        probe.assert_not_called()

    def test_frpc_latest_log_is_preserved_in_status(self):
        process = {"pid": 88, "running": True, "owned": True, "message": "frpc 正在运行"}
        existing = {
            "required": True,
            "control_host": "api.example.com",
            "control_port": 7010,
            "proxy_count": 1,
            "login_state": "success",
            "tunnel_state": "success",
            "latest_log": "start proxy success",
            "latest_log_at": "2026-07-14T07:00:00Z",
        }
        with mock.patch.object(agent, "frpc_process_state", return_value=process), mock.patch.object(
            agent, "tcp_endpoint_ok", return_value=(True, "FRP 控制端口可达")
        ):
            status = agent.compute_frpc_status(existing)
        self.assertEqual(status["pid"], 88)
        self.assertEqual(status["latest_log"], "start proxy success")
        self.assertEqual(status["latest_log_at"], "2026-07-14T07:00:00Z")

    def test_non_agent_frpc_config_is_rejected_without_touching_hk_frpc(self):
        hk_config = Path("/etc/frp/hk-frpc.ini")
        self.assertIn("拒绝", agent.frpc_config_error(hk_config))
        with mock.patch.object(agent, "effective_frpc_spec", return_value=(Path("/tmp/hk.pid"), hk_config)):
            status = agent.compute_frpc_status({"required": True, "control_host": "frps.example.com", "control_port": 7010})
        self.assertFalse(status["ok"])
        self.assertIn("拒绝", status["process_message"])

        with mock.patch.object(agent, "uci_get", return_value="/usr/bin/frpc"), mock.patch.object(
            agent, "effective_frpc_spec", return_value=(Path("/tmp/hk.pid"), hk_config)
        ):
            with self.assertRaisesRegex(RuntimeError, "拒绝"):
                agent.start_frpc()

    def test_hk_frpc_spec_is_excluded_from_owned_stop_candidates(self):
        hk_config = Path("/etc/frp/hk-frpc.ini")
        with mock.patch.object(agent, "effective_frpc_spec", return_value=(Path("/tmp/hk.pid"), hk_config)), mock.patch.object(
            agent, "package_get", side_effect=lambda package, option, default="": default
        ):
            specs = agent.all_owned_frpc_specs()
        self.assertNotIn((Path("/tmp/hk.pid"), hk_config), specs)
        self.assertIn((agent.DEFAULT_FRPC_PID, agent.DEFAULT_FRPC_CONFIG), specs)

    def test_socks_exit_probe_sends_and_reads_http_traffic(self):
        class FakeSocket:
            def __init__(self):
                self.sent = []
                self.closed = False

            def sendall(self, value):
                self.sent.append(value)

            def recv(self, size):
                del size
                return b"HTTP/1.1 204 No Content\r\n"

            def close(self):
                self.closed = True

        sock = FakeSocket()
        with mock.patch.object(agent, "socks5_open_tunnel", return_value=(sock, "")):
            ok, message = agent.socks5_connect_ok(
                "127.0.0.1", 1080, "api.example.com", 443, 5, protocol="http", request_path="/"
            )
        self.assertTrue(ok)
        self.assertIn("HTTP", message)
        self.assertTrue(sock.sent[0].startswith(b"HEAD / HTTP/1.1"))
        self.assertTrue(sock.closed)

    def test_health_check_keeps_exit_failure_separate_from_agent_health(self):
        report = {
            "socks": [
                {
                    "name": "SS-1",
                    "openwrt_port": 1080,
                    "exit_status": "unreachable",
                    "exit_message": "外部上游不可达",
                }
            ]
        }
        desired = {"enabled": True, "mappings": []}
        output = io.StringIO()
        with mock.patch.object(agent, "cfg_bool", return_value=True), mock.patch.object(
            agent, "probe_xiass_api", return_value=(True, "面板可达", 200, 12)
        ), mock.patch.object(agent, "build_report", return_value=report), mock.patch.object(
            agent, "api_request", side_effect=[{}, desired]
        ), mock.patch("sys.stdout", output):
            self.assertEqual(agent.health_check(), 0)
        result = json.loads(output.getvalue())
        self.assertTrue(result["ok"])
        self.assertFalse(result["exit"]["ok"])
        self.assertTrue(result["report"]["ok"])
        self.assertTrue(result["pull"]["ok"])

    def test_url_display_drops_credentials_and_query(self):
        value = agent.safe_url_display("https://user:pass@example.com:8443/base?token=secret#x")
        self.assertEqual(value, "https://example.com:8443/base")


if __name__ == "__main__":
    unittest.main()
