import AppKit
import Foundation

final class XIASSAuthorizationHelperApp: NSObject, NSApplicationDelegate {
    private let statusItem = NSStatusBar.system.statusItem(withLength: NSStatusItem.squareLength)
    private let statusMenuItem = NSMenuItem(title: "正在检查后台服务...", action: nil, keyEquivalent: "")
    private var timer: Timer?

    func applicationDidFinishLaunching(_ notification: Notification) {
        NSApp.setActivationPolicy(.accessory)
        configureStatusItem()
        let firstRun = !FileManager.default.fileExists(atPath: configPath)
        installResidentService()
        refreshStatus()
        timer = Timer.scheduledTimer(withTimeInterval: 5, repeats: true) { [weak self] _ in
            self?.refreshStatus()
        }
        if firstRun {
            DispatchQueue.main.asyncAfter(deadline: .now() + 1) { [weak self] in self?.openSetup() }
        }
    }

    func applicationWillTerminate(_ notification: Notification) {
        timer?.invalidate()
    }

    private var configPath: String {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Application Support/XIASS/adspower-helper/config.json").path
    }

    private var logURL: URL {
        FileManager.default.homeDirectoryForCurrentUser
            .appendingPathComponent("Library/Logs/XIASS/adspower-helper.log")
    }

    private func configureStatusItem() {
        if let button = statusItem.button {
            button.image = NSImage(systemSymbolName: "shield.lefthalf.filled", accessibilityDescription: "XIASS 授权助手")
            button.toolTip = "XIASS 授权助手"
        }
        let menu = NSMenu()
        let title = NSMenuItem(title: "XIASS 授权助手", action: nil, keyEquivalent: "")
        title.isEnabled = false
        menu.addItem(title)
        menu.addItem(statusMenuItem)
        menu.addItem(.separator())
        menu.addItem(NSMenuItem(title: "打开设置", action: #selector(openSetup), keyEquivalent: ","))
        menu.addItem(NSMenuItem(title: "重新安装并重启后台服务", action: #selector(reinstallService), keyEquivalent: "r"))
        menu.addItem(NSMenuItem(title: "查看运行日志", action: #selector(openLog), keyEquivalent: "l"))
        menu.addItem(.separator())
        menu.addItem(NSMenuItem(title: "退出菜单栏（后台继续运行）", action: #selector(quitMenuBar), keyEquivalent: "q"))
        for item in menu.items { item.target = self }
        statusItem.menu = menu
    }

    private func installResidentService() {
        guard let resources = Bundle.main.resourceURL else {
            showError("安装文件不完整", "应用内缺少 XIASS 后台服务。")
            return
        }
        let worker = resources.appendingPathComponent("xiass-adspower-helper")
        guard FileManager.default.isExecutableFile(atPath: worker.path) else {
            showError("安装文件不完整", "应用内的 XIASS 后台服务不可执行。")
            return
        }
        let process = Process()
        process.executableURL = worker
        process.arguments = ["install"]
        let output = Pipe()
        process.standardOutput = output
        process.standardError = output
        do {
            try process.run()
            process.waitUntilExit()
            guard process.terminationStatus == 0 else {
                let data = output.fileHandleForReading.readDataToEndOfFile()
                let message = String(data: data, encoding: .utf8) ?? "后台服务安装失败。"
                showError("无法安装 XIASS 授权助手", message)
                return
            }
        } catch {
            showError("无法安装 XIASS 授权助手", error.localizedDescription)
        }
    }

    private func refreshStatus() {
        guard let url = URL(string: "http://127.0.0.1:34987/healthz") else { return }
        var request = URLRequest(url: url)
        request.timeoutInterval = 3
        URLSession.shared.dataTask(with: request) { [weak self] _, response, _ in
            let online = (response as? HTTPURLResponse)?.statusCode == 200
            DispatchQueue.main.async {
                self?.statusMenuItem.title = online ? "状态：在线，等待授权任务" : "状态：后台已启动，等待 AdsPower"
                self?.statusItem.button?.contentTintColor = online ? .systemGreen : .systemOrange
            }
        }.resume()
    }

    @objc private func openSetup() {
        if let url = URL(string: "http://127.0.0.1:34987/setup") {
            NSWorkspace.shared.open(url)
        }
    }

    @objc private func reinstallService() {
        installResidentService()
        DispatchQueue.main.asyncAfter(deadline: .now() + 1) { [weak self] in self?.refreshStatus() }
    }

    @objc private func openLog() {
        let directory = logURL.deletingLastPathComponent()
        try? FileManager.default.createDirectory(at: directory, withIntermediateDirectories: true)
        if !FileManager.default.fileExists(atPath: logURL.path) {
            FileManager.default.createFile(atPath: logURL.path, contents: nil)
        }
        NSWorkspace.shared.open(logURL)
    }

    @objc private func quitMenuBar() {
        NSApp.terminate(nil)
    }

    private func showError(_ title: String, _ message: String) {
        DispatchQueue.main.async {
            let alert = NSAlert()
            alert.alertStyle = .critical
            alert.messageText = title
            alert.informativeText = message
            alert.runModal()
        }
    }
}

let application = NSApplication.shared
let applicationDelegate = XIASSAuthorizationHelperApp()
application.delegate = applicationDelegate
application.run()
