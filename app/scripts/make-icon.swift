// Draws the app icon into an .iconset folder: usage `swift make-icon.swift OUTPUT.iconset`.
import AppKit

let output = URL(fileURLWithPath: CommandLine.arguments[1])
try FileManager.default.createDirectory(at: output, withIntermediateDirectories: true)

func render(size: Int) -> Data {
    let pixels = CGFloat(size)
    let image = NSImage(size: NSSize(width: pixels, height: pixels))
    image.lockFocus()
    let inset = pixels * 0.1
    let rect = NSRect(x: inset, y: inset, width: pixels - 2 * inset, height: pixels - 2 * inset)
    let path = NSBezierPath(roundedRect: rect, xRadius: rect.width * 0.225, yRadius: rect.width * 0.225)
    NSGradient(starting: NSColor(calibratedRed: 0.13, green: 0.52, blue: 0.46, alpha: 1),
               ending: NSColor(calibratedRed: 0.07, green: 0.25, blue: 0.38, alpha: 1))!
        .draw(in: path, angle: -90)
    let config = NSImage.SymbolConfiguration(pointSize: rect.width * 0.5, weight: .semibold)
        .applying(.init(paletteColors: [.white]))
    if let symbol = NSImage(systemSymbolName: "shippingbox.fill", accessibilityDescription: nil)?.withSymbolConfiguration(config) {
        let s = symbol.size
        symbol.draw(in: NSRect(x: rect.midX - s.width / 2, y: rect.midY - s.height / 2, width: s.width, height: s.height))
    }
    image.unlockFocus()
    let bitmap = NSBitmapImageRep(data: image.tiffRepresentation!)!
    return bitmap.representation(using: .png, properties: [:])!
}

for base in [16, 32, 128, 256, 512] {
    try render(size: base).write(to: output.appendingPathComponent("icon_\(base)x\(base).png"))
    try render(size: base * 2).write(to: output.appendingPathComponent("icon_\(base)x\(base)@2x.png"))
}
