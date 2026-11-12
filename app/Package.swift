// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "StackNest",
    platforms: [.macOS(.v14)],
    targets: [
        .executableTarget(
            name: "StackNest",
            path: "Sources/StackNest"
        ),
    ],
    swiftLanguageModes: [.v5]
)
