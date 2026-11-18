import AppKit
import WebKit

/// Builds the menu bar. The app has no nib, so the standard menus, and
/// with them the usual keyboard shortcuts, are put together here.
@MainActor
enum MainMenu {
    /// One entry of a menu: its title, what it does and its shortcut.
    private struct Entry {
        /// The title shown in the menu.
        let title: String
        /// The action sent up the responder chain.
        let action: Selector
        /// The key pressed with Command, or empty for no shortcut.
        var key = ""
        /// The modifiers pressed with the key.
        var modifiers: NSEvent.ModifierFlags = .command
    }

    /// Gives app its menu bar and tells it which menu lists the windows.
    static func install(in app: NSApplication, name: String) {
        let windows = menu("Window", windowEntries)
        let bar = NSMenu()
        let about = menu(name, appEntries(name))
        for submenu in [about, menu("Edit", editEntries), menu("View", viewEntries), windows] {
            let item = NSMenuItem()
            item.submenu = submenu
            bar.addItem(item)
        }
        app.mainMenu = bar
        app.windowsMenu = windows
    }

    /// Returns a menu made of groups of entries with separators between them.
    private static func menu(_ title: String, _ groups: [[Entry]]) -> NSMenu {
        let menu = NSMenu(title: title)
        for (index, group) in groups.enumerated() {
            if index > 0 {
                menu.addItem(.separator())
            }
            for entry in group {
                let item = menu.addItem(
                    withTitle: entry.title, action: entry.action, keyEquivalent: entry.key)
                item.keyEquivalentModifierMask = entry.modifiers
            }
        }
        return menu
    }

    /// The entries of the application menu.
    private static func appEntries(_ name: String) -> [[Entry]] {
        [
            [
                Entry(
                    title: "About \(name)",
                    action: #selector(NSApplication.orderFrontStandardAboutPanel(_:)))
            ],
            [
                Entry(title: "Hide \(name)", action: #selector(NSApplication.hide(_:)), key: "h"),
                Entry(
                    title: "Hide Others", action: #selector(NSApplication.hideOtherApplications(_:)),
                    key: "h", modifiers: [.command, .option]),
                Entry(title: "Show All", action: #selector(NSApplication.unhideAllApplications(_:))),
            ],
            [Entry(title: "Quit \(name)", action: #selector(NSApplication.terminate(_:)), key: "q")],
        ]
    }

    /// The entries of the Edit menu, which make the text shortcuts work.
    private static let editEntries: [[Entry]] = [
        [
            Entry(title: "Undo", action: Selector(("undo:")), key: "z"),
            Entry(title: "Redo", action: Selector(("redo:")), key: "z", modifiers: [.command, .shift]),
        ],
        [
            Entry(title: "Cut", action: #selector(NSText.cut(_:)), key: "x"),
            Entry(title: "Copy", action: #selector(NSText.copy(_:)), key: "c"),
            Entry(title: "Paste", action: #selector(NSText.paste(_:)), key: "v"),
            Entry(title: "Select All", action: #selector(NSText.selectAll(_:)), key: "a"),
        ],
    ]

    /// The entries of the View menu, which the web view answers.
    private static let viewEntries: [[Entry]] = [
        [
            Entry(title: "Back", action: #selector(WKWebView.goBack(_:)), key: "["),
            Entry(title: "Forward", action: #selector(WKWebView.goForward(_:)), key: "]"),
            Entry(title: "Reload", action: #selector(WKWebView.reload(_:)), key: "r"),
        ],
        [
            Entry(
                title: "Enter Full Screen", action: #selector(NSWindow.toggleFullScreen(_:)), key: "f",
                modifiers: [.command, .control])
        ],
    ]

    /// The entries of the Window menu.
    private static let windowEntries: [[Entry]] = [
        [
            Entry(title: "Minimize", action: #selector(NSWindow.performMiniaturize(_:)), key: "m"),
            Entry(title: "Zoom", action: #selector(NSWindow.performZoom(_:))),
        ]
    ]
}
