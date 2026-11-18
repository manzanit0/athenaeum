import Foundation

/// The Athenaeum server, run as a child process of the app and reached
/// over a loopback port only this app knows.
@MainActor
final class Server {
    /// The ways the server can fail to come up.
    enum Failure: LocalizedError {
        /// The server binary is missing from the app bundle.
        case missingBinary
        /// No loopback port could be reserved.
        case noPort
        /// The server exited; the text is the end of its log.
        case stopped(String)

        /// A sentence describing the failure.
        var errorDescription: String? {
            switch self {
            case .missingBinary:
                return "The server is missing from the app. Build the app again."
            case .noPort:
                return "No free network port was found on this Mac."
            case .stopped(let log):
                return log.isEmpty ? "The server stopped without saying why." : log
            }
        }
    }

    /// The name of the server binary inside the bundle, next to the app's own.
    private static let binaryName = "athenaeum-server"

    /// Where calibre installs its command line tools.
    private static let calibreTools = "/Applications/calibre.app/Contents/MacOS"

    /// How often the server is asked whether it is up.
    private static let pollInterval: UInt64 = 200_000_000

    /// How much of the end of the log is shown when the server fails.
    private static let logTailBytes = 1500

    /// The address of the web interface.
    let url: URL

    /// The file the server's output is written to.
    let logFile: URL

    /// Called on the main actor when the server exits without being asked to.
    var onUnexpectedExit: (() -> Void)?

    /// The server process.
    private let process = Process()

    /// A pipe into the server's standard input that is never written to.
    /// The server stops when it closes, which happens even if this app
    /// is killed.
    private let lifeline = Pipe()

    /// Whether the app asked the server to stop.
    private var stopping = false

    /// Starts the server on a free loopback port, with its data in
    /// Application Support and its output in a log file.
    init() throws {
        guard let binary = Bundle.main.url(forAuxiliaryExecutable: Server.binaryName) else {
            throw Failure.missingBinary
        }
        let port = try Server.freePort()
        url = URL(string: "http://127.0.0.1:\(port)/")!
        logFile = try Server.makeLogFile()
        let log = try FileHandle(forWritingTo: logFile)
        process.executableURL = binary
        process.environment = try Server.environment(port: port)
        process.standardInput = lifeline
        process.standardOutput = log
        process.standardError = log
        process.terminationHandler = { [weak self] _ in
            Task { @MainActor in self?.exited() }
        }
        try process.run()
    }

    /// Returns once the server answers, and throws if it exits first.
    func waitUntilReady() async throws {
        let health = url.appendingPathComponent("api/health")
        while process.isRunning {
            if await Server.answers(health) {
                return
            }
            try await Task.sleep(nanoseconds: Server.pollInterval)
        }
        throw Failure.stopped(logTail())
    }

    /// Asks the server to stop and waits until it has.
    func stop() {
        stopping = true
        guard process.isRunning else {
            return
        }
        try? lifeline.fileHandleForWriting.close()
        process.terminate()
        process.waitUntilExit()
    }

    /// Returns the end of the server's log.
    func logTail() -> String {
        guard let data = try? Data(contentsOf: logFile) else {
            return ""
        }
        let text = String(decoding: data.suffix(Server.logTailBytes), as: UTF8.self)
        return text.trimmingCharacters(in: .whitespacesAndNewlines)
    }

    /// Reports an exit nobody asked for.
    private func exited() {
        if !stopping {
            onUnexpectedExit?()
        }
    }

    /// Reports whether the server answers a health check.
    private static func answers(_ health: URL) async -> Bool {
        guard let (_, response) = try? await URLSession.shared.data(from: health) else {
            return false
        }
        return (response as? HTTPURLResponse)?.statusCode == 200
    }

    /// Builds the server's environment: what the app was started with, plus
    /// the address, the data folder and calibre's tools when they are found.
    /// A data folder or tool already set in the environment is kept.
    private static func environment(port: UInt16) throws -> [String: String] {
        var environment = ProcessInfo.processInfo.environment
        environment["ATHENAEUM_ADDR"] = "127.0.0.1:\(port)"
        environment["ATHENAEUM_STOP_ON_STDIN_CLOSE"] = "1"
        if environment["ATHENAEUM_DATA_DIR"] == nil {
            environment["ATHENAEUM_DATA_DIR"] = try dataFolder().path
        }
        let tools = ["ATHENAEUM_EBOOK_CONVERT": "ebook-convert", "ATHENAEUM_EBOOK_META": "ebook-meta"]
        for (key, tool) in tools where environment[key] == nil {
            let path = "\(calibreTools)/\(tool)"
            if FileManager.default.isExecutableFile(atPath: path) {
                environment[key] = path
            }
        }
        return environment
    }

    /// Returns the folder holding the library, creating it when missing.
    private static func dataFolder() throws -> URL {
        let support = try FileManager.default.url(
            for: .applicationSupportDirectory, in: .userDomainMask, appropriateFor: nil, create: true)
        let folder = support.appendingPathComponent("Athenaeum", isDirectory: true)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        return folder
    }

    /// Creates an empty log file for this run, replacing the last run's.
    private static func makeLogFile() throws -> URL {
        let library = try FileManager.default.url(
            for: .libraryDirectory, in: .userDomainMask, appropriateFor: nil, create: true)
        let folder = library.appendingPathComponent("Logs/Athenaeum", isDirectory: true)
        try FileManager.default.createDirectory(at: folder, withIntermediateDirectories: true)
        let file = folder.appendingPathComponent("server.log")
        try Data().write(to: file)
        return file
    }

    /// Finds a loopback port nothing listens on by binding to port zero
    /// and reading back the port the system chose.
    private static func freePort() throws -> UInt16 {
        let descriptor = socket(AF_INET, SOCK_STREAM, 0)
        guard descriptor >= 0 else {
            throw Failure.noPort
        }
        defer { close(descriptor) }
        var address = sockaddr_in()
        address.sin_len = UInt8(MemoryLayout<sockaddr_in>.size)
        address.sin_family = sa_family_t(AF_INET)
        address.sin_addr.s_addr = inet_addr("127.0.0.1")
        var length = socklen_t(MemoryLayout<sockaddr_in>.size)
        let bound = withUnsafeMutablePointer(to: &address) { pointer in
            pointer.withMemoryRebound(to: sockaddr.self, capacity: 1) { generic in
                bind(descriptor, generic, length) == 0 && getsockname(descriptor, generic, &length) == 0
            }
        }
        guard bound else {
            throw Failure.noPort
        }
        return UInt16(bigEndian: address.sin_port)
    }
}
