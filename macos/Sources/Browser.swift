import AppKit
import WebKit

/// The web view showing the interface, with what a browser would add
/// around it: file pickers, downloads, and links that leave the app.
@MainActor
final class Browser: NSObject {
    /// The view to put in a window.
    let view: WKWebView

    /// The address of the web interface.
    private let home: URL

    /// Creates the view without loading anything yet.
    init(home: URL) {
        self.home = home
        view = WKWebView(frame: .zero, configuration: WKWebViewConfiguration())
        super.init()
        view.navigationDelegate = self
        view.uiDelegate = self
        view.allowsBackForwardNavigationGestures = true
    }

    /// Loads the web interface.
    func open() {
        view.load(URLRequest(url: home))
    }

    /// Reports whether url is a web page outside the app's own server.
    private func isElsewhere(_ url: URL?) -> Bool {
        guard let url, let scheme = url.scheme, ["http", "https"].contains(scheme) else {
            return false
        }
        return url.host != home.host || url.port != home.port
    }

    /// Reports whether the server asked for response to be saved as a file.
    private func isAttachment(_ response: URLResponse) -> Bool {
        let header = (response as? HTTPURLResponse)?.value(forHTTPHeaderField: "Content-Disposition")
        return header?.lowercased().hasPrefix("attachment") ?? false
    }
}

extension Browser: WKNavigationDelegate {
    /// Sends pages outside the app to the default browser and turns
    /// download links into downloads.
    func webView(_ webView: WKWebView, decidePolicyFor navigationAction: WKNavigationAction) async
        -> WKNavigationActionPolicy
    {
        if navigationAction.shouldPerformDownload {
            return .download
        }
        if let url = navigationAction.request.url, isElsewhere(url) {
            NSWorkspace.shared.open(url)
            return .cancel
        }
        return .allow
    }

    /// Saves what the server marks as an attachment, or what the view
    /// cannot show, instead of displaying it.
    func webView(_ webView: WKWebView, decidePolicyFor navigationResponse: WKNavigationResponse) async
        -> WKNavigationResponsePolicy
    {
        if isAttachment(navigationResponse.response) || !navigationResponse.canShowMIMEType {
            return .download
        }
        return .allow
    }

    /// Takes charge of a download started by a link.
    func webView(
        _ webView: WKWebView, navigationAction: WKNavigationAction, didBecome download: WKDownload
    ) {
        download.delegate = self
    }

    /// Takes charge of a download started by a response.
    func webView(
        _ webView: WKWebView, navigationResponse: WKNavigationResponse, didBecome download: WKDownload
    ) {
        download.delegate = self
    }
}

extension Browser: WKDownloadDelegate {
    /// Asks where to save a download. WebKit only writes to a file that
    /// does not exist, so a file the reader chose to replace is removed.
    func download(
        _ download: WKDownload, decideDestinationUsing response: URLResponse, suggestedFilename: String
    ) async -> URL? {
        guard let window = view.window else {
            return nil
        }
        let panel = NSSavePanel()
        panel.nameFieldStringValue = suggestedFilename
        guard await panel.beginSheetModal(for: window) == .OK, let destination = panel.url else {
            return nil
        }
        try? FileManager.default.removeItem(at: destination)
        return destination
    }

    /// Tells the reader that a download did not finish.
    func download(_ download: WKDownload, didFailWithError error: Error, resumeData: Data?) {
        let alert = NSAlert()
        alert.messageText = "The download did not finish"
        alert.informativeText = error.localizedDescription
        alert.runModal()
    }
}

extension Browser: WKUIDelegate {
    /// Shows the file picker for the interface's upload buttons.
    func webView(
        _ webView: WKWebView, runOpenPanelWith parameters: WKOpenPanelParameters,
        initiatedByFrame frame: WKFrameInfo
    ) async -> [URL]? {
        guard let window = view.window else {
            return nil
        }
        let panel = NSOpenPanel()
        panel.allowsMultipleSelection = parameters.allowsMultipleSelection
        panel.canChooseDirectories = parameters.allowsDirectories
        return await panel.beginSheetModal(for: window) == .OK ? panel.urls : nil
    }

    /// Opens what the interface wants in a new tab in the default browser,
    /// because the app has a single window.
    func webView(
        _ webView: WKWebView, createWebViewWith configuration: WKWebViewConfiguration,
        for navigationAction: WKNavigationAction, windowFeatures: WKWindowFeatures
    ) -> WKWebView? {
        if let url = navigationAction.request.url {
            NSWorkspace.shared.open(url)
        }
        return nil
    }
}
