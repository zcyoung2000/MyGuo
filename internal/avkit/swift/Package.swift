// swift-tools-version: 6.0
import PackageDescription

let package = Package(
    name: "Avkit",
    platforms: [.macOS(.v13)],
    products: [.library(name: "Avkit", type: .dynamic, targets: ["Avkit"])],
    dependencies: [],
    targets: [
        .target(name: "Avkit"),
    ],
    swiftLanguageModes: [.v5]
)
