import SwiftUI

// Floodlight: Milo's palette. Dark only. Orange marks small things (done ticks,
// the active tab, the logo, text buttons, focus); sky blue is only for new bests.
extension Color {
    static let bg = Color(hex: 0x0A0E1A)
    static let s1 = Color(hex: 0x121929)
    static let s2 = Color(hex: 0x1A2236)
    static let s3 = Color(hex: 0x253049)
    static let well = Color(hex: 0x0C1120)
    static let line = Color.white.opacity(0.07)
    static let line2 = Color.white.opacity(0.12)
    static let tx = Color(hex: 0xE8EDF7)
    static let tx2 = Color(hex: 0x9BA6BD)
    static let tx3 = Color(hex: 0x626E88)
    static let or = Color(hex: 0xFF8F45)
    static let bl = Color(hex: 0x69B7FF)
    static let red = Color(hex: 0xFF7A70)

    init(hex: UInt32, opacity: Double = 1) {
        self.init(
            .sRGB,
            red: Double((hex >> 16) & 0xFF) / 255,
            green: Double((hex >> 8) & 0xFF) / 255,
            blue: Double(hex & 0xFF) / 255,
            opacity: opacity
        )
    }
}

enum Radius {
    static let card: CGFloat = 24
    static let well: CGFloat = 13
}

/// The gaps that keep a screen from feeling crowded, named once so every screen breathes alike.
enum Space {
    /// Between a large title and the line under it.
    static let underTitleText: CGFloat = 2
    /// Between a screen's own heading and the line under it.
    static let headingText: CGFloat = 8
    /// Between a row's name and the smaller line under it.
    static let rowText: CGFloat = 4
    /// Between that line and the screen's first card.
    static let underTitle: CGFloat = 20
}


extension UIFont {
    /// Sofia Sans for the UIKit pieces SwiftUI can't style, like navigation bar titles.
    static func sofia(_ size: CGFloat, _ weight: UIFont.Weight) -> UIFont {
        let descriptor = UIFontDescriptor(fontAttributes: [
            .family: "Sofia Sans",
            .traits: [UIFontDescriptor.TraitKey.weight: weight],
        ])
        return UIFont(descriptor: descriptor, size: size)
    }
}

// Milo's motion: quick and settled, never bouncy. Named once so every screen moves alike.
extension Animation {
    /// A control answering a touch: a tick, a switch, a key.
    static let quick = Animation.snappy(duration: 0.2)
    /// Something arriving, leaving or changing place.
    static let settle = Animation.smooth(duration: 0.32)
}

extension AnyTransition {
    /// How a row or card arrives: it fades up into place.
    static let arrive = AnyTransition.opacity.combined(with: .offset(y: 8))
}

// Sofia Sans at calm sizes (the canvas's type scale), scaling with Dynamic Type.
extension Font {
    static func sofia(_ size: CGFloat, _ weight: Font.Weight = .regular, relativeTo style: Font.TextStyle = .body) -> Font {
        .custom("Sofia Sans", size: size, relativeTo: style).weight(weight)
    }

    /// Screen titles: "Today", "Progress".
    static let large = sofia(34, .bold, relativeTo: .largeTitle)
    /// Pushed screen titles: "Legs", "Chest Press".
    static let title = sofia(26, .bold, relativeTo: .title)
    /// Section headers: "Templates", "Recent".
    static let h2 = sofia(20, .bold, relativeTo: .title3)
    /// Card and row titles.
    static let h3 = sofia(17, .semibold, relativeTo: .headline)
    static let body16 = sofia(16, .regular, relativeTo: .body)
    static let bodyStrong = sofia(16, .semibold, relativeTo: .body)
    /// Secondary lines.
    static let sub = sofia(14.5, .regular, relativeTo: .subheadline)
    /// Small labels.
    static let cap = sofia(12.5, .semibold, relativeTo: .caption)
    static let button = sofia(17, .bold, relativeTo: .headline)
    static let buttonSmall = sofia(15, .bold, relativeTo: .subheadline)
    static let link = sofia(15.5, .semibold, relativeTo: .subheadline)
}

extension View {
    /// The one card surface.
    func card(padding: CGFloat = 18) -> some View {
        self.padding(padding)
            .background(Color.s1, in: .rect(cornerRadius: Radius.card))
            .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
    }

    /// Screen background that runs under the bars. The ground deepens toward the top and
    /// bottom edges, where the glass sits. Every piece of glass in the app is Apple's one
    /// standard glass and takes its colour from what's behind it, so over this it all reads
    /// as the same dark glass instead of a grey slab.
    func miloScreen() -> some View {
        self.background {
            Color.bg
                .overlay(alignment: .top) {
                    LinearGradient(colors: [.black.opacity(0.7), .clear], startPoint: .top, endPoint: .bottom)
                        .frame(height: 200)
                }
                .overlay(alignment: .bottom) {
                    LinearGradient(colors: [.clear, .black.opacity(0.85)], startPoint: .top, endPoint: .bottom)
                        .frame(height: 260)
                }
                .ignoresSafeArea()
        }
        .foregroundStyle(Color.tx)
    }
}
