package service

import (
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestExecutionNodeSOCKSBindAddressDefaultsAndValidation(t *testing.T) {
	for _, host := range []string{"", "127.0.0.1", "0.0.0.0", "192.0.2.1", "::", "localhost", "0.0.0.0:19080"} {
		t.Run("host="+host, func(t *testing.T) {
			t.Setenv(ExecutionNodeSOCKSBindHostEnv, host)
			address, err := executionNodeSOCKSListenAddress()
			switch host {
			case "", "127.0.0.1":
				require.NoError(t, err)
				require.Equal(t, "127.0.0.1:19080", address)
			case "0.0.0.0":
				require.NoError(t, err)
				require.Equal(t, "0.0.0.0:19080", address)
			default:
				require.Error(t, err)
				require.Empty(t, address)
			}
		})
	}
}

func TestExecutionNodeSOCKSBindDoesNotEnableRuntime(t *testing.T) {
	t.Setenv(ExecutionNodeSOCKSBindHostEnv, "0.0.0.0")
	t.Setenv(ExecutionNodeTunnelTokenEnv, "")
	t.Setenv("GATEWAY_EXECUTION_NODE_ENABLED", "false")
	t.Setenv("GATEWAY_EXECUTION_NODE_ID", "primary")
	t.Setenv("JWT_SECRET", strings.Repeat("fixture", 8))
	runtime, err := StartExecutionNodeTunnelRuntimeFromEnv()
	require.NoError(t, err)
	if runtime != nil {
		_ = runtime.Close()
		t.Fatal("bind opt-in must not start a disabled execution-node runtime")
	}
}

func TestExecutionNodeSOCKSListenerNativeAndDockerKeepAuthentication(t *testing.T) {
	for _, host := range []string{"", "0.0.0.0"} {
		t.Run("host="+host, func(t *testing.T) {
			t.Setenv(ExecutionNodeSOCKSBindHostEnv, host)
			t.Setenv(ExecutionNodeTunnelTokenEnv, strings.Repeat("a", 64))
			t.Setenv("GATEWAY_EXECUTION_NODE_ENABLED", "true")
			t.Setenv("GATEWAY_EXECUTION_NODE_ID", "primary")
			t.Setenv(ExecutionNodeStateSourceURLEnv, "")
			t.Setenv(ExecutionNodeNodeURLsEnv, "")
			runtime, err := StartExecutionNodeTunnelRuntimeFromEnv()
			require.NoError(t, err)
			require.NotNil(t, runtime)
			t.Cleanup(func() { require.NoError(t, runtime.Close()) })
			require.Len(t, runtime.listeners, 1, "no state tunnels should start")
			bound, ok := runtime.listeners[0].Addr().(*net.TCPAddr)
			require.True(t, ok)
			require.Equal(t, 19080, bound.Port)
			if host == "" {
				require.True(t, bound.IP.IsLoopback())
			} else {
				require.True(t, bound.IP.IsUnspecified())
			}
			connect := func(t *testing.T) net.Conn {
				t.Helper()
				conn, err := net.DialTimeout("tcp", executionNodeEgressSOCKSAddress, time.Second)
				require.NoError(t, err)
				t.Cleanup(func() { _ = conn.Close() })
				require.NoError(t, conn.SetDeadline(time.Now().Add(3*time.Second)))
				return conn
			}
			read := func(t *testing.T, conn net.Conn, expected []byte) {
				t.Helper()
				actual := make([]byte, len(expected))
				_, err := io.ReadFull(conn, actual)
				require.NoError(t, err)
				require.Equal(t, expected, actual)
			}
			t.Run("reject anonymous", func(t *testing.T) {
				conn := connect(t)
				_, err := conn.Write([]byte{5, 1, 0})
				require.NoError(t, err)
				read(t, conn, []byte{5, 255})
			})
			for _, valid := range []bool{false, true} {
				name, token, status := "reject wrong token", strings.Repeat("b", 64), byte(1)
				if valid {
					name, token, status = "authenticated egress", strings.Repeat("a", 64), 0
				}
				t.Run(name, func(t *testing.T) {
					conn := connect(t)
					_, err := conn.Write([]byte{5, 2, 0, 2})
					require.NoError(t, err)
					read(t, conn, []byte{5, 2})
					auth := append([]byte{1, 7}, []byte("primary")...)
					auth = append(auth, byte(len(token)))
					_, err = conn.Write(append(auth, []byte(token)...))
					require.NoError(t, err)
					read(t, conn, []byte{1, status})
					if !valid {
						_, err = conn.Read(make([]byte, 1))
						require.ErrorIs(t, err, io.EOF)
						return
					}
					target, err := net.Listen("tcp", "127.0.0.1:0")
					require.NoError(t, err)
					defer func() { _ = target.Close() }()
					done := make(chan struct{})
					go func() {
						defer close(done)
						accepted, err := target.Accept()
						if err != nil {
							return
						}
						defer func() { _ = accepted.Close() }()
						_ = accepted.SetDeadline(time.Now().Add(3 * time.Second))
						_, _ = io.Copy(accepted, accepted)
					}()
					t.Cleanup(func() { _ = target.Close(); <-done })
					targetAddr, ok := target.Addr().(*net.TCPAddr)
					require.True(t, ok)
					port := targetAddr.Port
					// A domain request verifies that DNS is handled at this SOCKS
					// endpoint without changing the local-node routing decision.
					request := append([]byte{5, 1, 0, 3, 9}, []byte("localhost")...)
					_, err = conn.Write(append(request, byte(port>>8), byte(port)))
					require.NoError(t, err)
					read(t, conn, []byte{5, 0, 0, 1, 0, 0, 0, 0, 0, 0})
					_, err = conn.Write([]byte("selected-egress"))
					require.NoError(t, err)
					read(t, conn, []byte("selected-egress"))
					require.NoError(t, conn.Close())
				})
			}
		})
	}
}
