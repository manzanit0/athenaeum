import AppKit

/// The app: one window showing the web interface of a server it starts
/// when it opens and stops when it quits. Which library is open, and
/// where libraries are kept, is the interface's business, not the app's.
@main
@MainActor
final class AppDelegate: NSObject, NSApplicationDelegate, NSWindowDelegate {
    /// The name shown in the menu bar and the window title.
    private static let name = "Athenaeum"

    /// The size of the window the first time the app opens.
    private static let firstSize = NSSize(width: 1280, height: 860)

    /// The smallest size the window can be dragged to.
    private static let smallestSize = NSSize(width: 720, height: 480)

    /// The server behind the window.
    private var server: Server?

    /// The web view, once the server is up.
    private var browser: Browser?

    /// The app's only window.
    private var window: NSWindow?

    /// Runs the app. There is no nib, so the delegate is set by hand.
    static func main() {
        let app = NSApplication.shared
        let delegate = AppDelegate()
        app.delegate = delegate
        app.setActivationPolicy(.regular)
        app.run()
    }

    /// Opens the window, starts the server and shows the interface once
    /// the server answers.
    func applicationDidFinishLaunching(_ notification: Notification) {
        MainMenu.install(in: NSApp, name: AppDelegate.name)
        let window = makeWindow()
        self.window = window
        NSApp.activate(ignoringOtherApps: true)
        Task {
            do {
                try await start(in: window)
            } catch {
                fail("\(AppDelegate.name) could not start", error.localizedDescription)
            }
        }
    }

    /// Stops the server before the app exits.
    func applicationWillTerminate(_ notification: Notification) {
        server?.stop()
    }

    /// Quits when the window closes, since the app is that window.
    func windowWillClose(_ notification: Notification) {
        NSApp.terminate(nil)
    }

    /// Starts the server, waits for it and puts its interface in window.
    private func start(in window: NSWindow) async throws {
        let server = try Server()
        self.server = server
        try await server.waitUntilReady()
        server.onUnexpectedExit = { [weak self] in
            self?.fail("The server behind \(AppDelegate.name) stopped", server.logTail())
        }
        let browser = Browser(home: server.url)
        self.browser = browser
        window.contentView = browser.view
        window.makeFirstResponder(browser.view)
        browser.open()
    }

    /// Creates the window with a line of text shown while the server
    /// starts.
    private func makeWindow() -> NSWindow {
        let window = NSWindow(
            contentRect: NSRect(origin: .zero, size: AppDelegate.firstSize),
            styleMask: [.titled, .closable, .miniaturizable, .resizable],
            backing: .buffered, defer: false)
        window.title = AppDelegate.name
        window.minSize = AppDelegate.smallestSize
        window.isReleasedWhenClosed = false
        window.delegate = self
        window.contentView = waitingView()
        window.center()
        window.setFrameAutosaveName("Library")
        window.makeKeyAndOrderFront(nil)
        return window
    }

    /// Returns the view shown until the interface is ready.
    private func waitingView() -> NSView {
        let label = NSTextField(labelWithString: "Opening your library…")
        label.textColor = .secondaryLabelColor
        label.translatesAutoresizingMaskIntoConstraints = false
        let view = NSView()
        view.addSubview(label)
        NSLayoutConstraint.activate([
            label.centerXAnchor.constraint(equalTo: view.centerXAnchor),
            label.centerYAnchor.constraint(equalTo: view.centerYAnchor),
        ])
        return view
    }

    /// Says what went wrong, where the log is, and quits.
    private func fail(_ message: String, _ detail: String) {
        let alert = NSAlert()
        alert.alertStyle = .critical
        alert.messageText = message
        alert.informativeText = [detail, server.map { "The full log is at \($0.logFile.path)." }]
            .compactMap { $0 }
            .joined(separator: "\n\n")
        alert.runModal()
        NSApp.terminate(nil)
    }
}
