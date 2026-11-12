#if DEBUG
import AppKit

/// Development aid: STACKNEST_SNAPSHOT=/path/prefix makes a debug build save
/// a PNG of its window for each section, then quit. It draws the app's own
/// window, so it needs no screen-recording permission.
enum DebugSnapshot {
    static var prefix: String? { ProcessInfo.processInfo.environment["STACKNEST_SNAPSHOT"] }

    @MainActor
    static func capture(sections: [String], select: @escaping (String) -> Void) {
        guard let prefix else { return }
        Task { @MainActor in
            try? await Task.sleep(for: .seconds(4))
            for section in sections {
                select(section)
                try? await Task.sleep(for: .seconds(section == "home" ? 25 : 3))
                guard let window = NSApp.windows.first(where: { $0.isVisible && $0.contentView != nil && $0.frame.width > 500 }),
                      let view = window.contentView,
                      let rep = view.bitmapImageRepForCachingDisplay(in: view.bounds) else { continue }
                view.cacheDisplay(in: view.bounds, to: rep)
                try? rep.representation(using: .png, properties: [:])?.write(to: URL(fileURLWithPath: "\(prefix)-\(section).png"))
            }
            NSApp.terminate(nil)
        }
    }
}
#endif
