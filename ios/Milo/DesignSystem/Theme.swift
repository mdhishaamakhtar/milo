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

// Sofia Sans at calm sizes, scaling with Dynamic Type. Numbers use tabular figures.
extension Font {
    static func sofia(_ size: CGFloat, _ weight: Font.Weight = .regular, relativeTo style: Font.TextStyle = .body) -> Font {
        .custom("Sofia Sans", size: size, relativeTo: style).weight(weight)
    }

    static let largeTitleMilo = sofia(34, .bold, relativeTo: .largeTitle)
    static let titleMilo = sofia(22, .bold, relativeTo: .title2)
    static let headlineMilo = sofia(19, .semibold, relativeTo: .headline)
    static let bodyMilo = sofia(17, .regular, relativeTo: .body)
    static let bodyStrong = sofia(17, .semibold, relativeTo: .body)
    static let calloutMilo = sofia(15, .regular, relativeTo: .callout)
    static let calloutStrong = sofia(15, .semibold, relativeTo: .callout)
    static let footMilo = sofia(13, .regular, relativeTo: .footnote)
    static let footStrong = sofia(13, .semibold, relativeTo: .footnote)
    static let captionMilo = sofia(12, .medium, relativeTo: .caption)
}

extension View {
    /// The one card surface.
    func card(padding: CGFloat = 18) -> some View {
        self.padding(padding)
            .background(Color.s1, in: .rect(cornerRadius: Radius.card))
            .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
    }

    /// Screen background that runs under the bars.
    func miloScreen() -> some View {
        self.background(Color.bg.ignoresSafeArea())
            .foregroundStyle(Color.tx)
    }
}

/// Off-white primary button with dark text, full width.
struct PrimaryButtonStyle: ButtonStyle {
    var compact = false
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(.bodyStrong)
            .foregroundStyle(Color.bg)
            .frame(maxWidth: .infinity, minHeight: compact ? 44 : 52)
            .background(Color.tx.opacity(configuration.isPressed ? 0.85 : 1), in: .capsule)
            .scaleEffect(configuration.isPressed ? 0.98 : 1)
            .animation(.snappy(duration: 0.18), value: configuration.isPressed)
    }
}

/// Quiet capsule on the card surface.
struct SecondaryButtonStyle: ButtonStyle {
    var compact = false
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(compact ? .calloutStrong : .bodyStrong)
            .foregroundStyle(Color.tx)
            .padding(.horizontal, compact ? 14 : 18)
            .frame(minHeight: compact ? 36 : 52)
            .background(Color.s2.opacity(configuration.isPressed ? 0.7 : 1), in: .capsule)
            .overlay(Capsule().strokeBorder(Color.line))
            .scaleEffect(configuration.isPressed ? 0.98 : 1)
            .animation(.snappy(duration: 0.18), value: configuration.isPressed)
    }
}

extension ButtonStyle where Self == PrimaryButtonStyle {
    static var primary: PrimaryButtonStyle { PrimaryButtonStyle() }
    static var primaryCompact: PrimaryButtonStyle { PrimaryButtonStyle(compact: true) }
}

extension ButtonStyle where Self == SecondaryButtonStyle {
    static var secondary: SecondaryButtonStyle { SecondaryButtonStyle() }
    static var chip: SecondaryButtonStyle { SecondaryButtonStyle(compact: true) }
}

/// Section title with an optional orange text button: "Templates · New".
struct SectionHeader<Trailing: View>: View {
    let title: String
    @ViewBuilder var trailing: Trailing

    init(_ title: String, @ViewBuilder trailing: () -> Trailing = { EmptyView() }) {
        self.title = title
        self.trailing = trailing()
    }

    var body: some View {
        HStack(alignment: .firstTextBaseline) {
            Text(title).font(.headlineMilo)
            Spacer()
            trailing.font(.calloutStrong).tint(.or)
        }
        .padding(.horizontal, 4)
    }
}

/// Small rounded label: "Approximate", "Proposed", "Avg 2.3".
struct Badge: View {
    let text: String
    var color: Color = .or

    var body: some View {
        Text(text)
            .font(.captionMilo)
            .foregroundStyle(color)
            .padding(.horizontal, 8)
            .padding(.vertical, 3)
            .background(color.opacity(0.14), in: .rect(cornerRadius: 6))
    }
}

/// Milo's mark: four bars spelling an M that ends on the tallest.
struct BarMLogo: View {
    var size: CGFloat = 16
    var color: Color = .or

    var body: some View {
        HStack(alignment: .bottom, spacing: size * 0.09) {
            ForEach([0.62, 0.42, 0.74, 1.0], id: \.self) { h in
                RoundedRectangle(cornerRadius: size * 0.05)
                    .fill(color)
                    .frame(width: size * 0.16, height: size * h)
            }
        }
        .frame(width: size, height: size, alignment: .bottom)
        .accessibilityHidden(true)
    }
}

/// The round avatar that opens Profile.
struct AvatarButton: View {
    let initial: String
    let action: () -> Void

    var body: some View {
        Button(action: action) {
            Text(initial)
                .font(.calloutStrong)
                .frame(width: 40, height: 40)
                .background(Color.s2, in: .circle)
                .overlay(Circle().strokeBorder(Color.line2))
        }
        .buttonStyle(.plain)
        .accessibilityLabel("Profile")
    }
}

/// A round tick: orange when done.
struct DoneTick: View {
    let done: Bool
    var size: CGFloat = 32

    var body: some View {
        ZStack {
            Circle()
                .fill(done ? Color.or : Color.clear)
                .overlay(Circle().strokeBorder(done ? Color.clear : Color.line2, lineWidth: 1.5))
            if done {
                Image(systemName: "checkmark")
                    .font(.system(size: size * 0.42, weight: .bold))
                    .foregroundStyle(Color.bg)
                    .transition(.scale(scale: 0.5).combined(with: .opacity))
            }
        }
        .frame(width: size, height: size)
        .animation(.snappy(duration: 0.2), value: done)
    }
}

/// kg | lb switch.
struct UnitSwitch: View {
    @Binding var unit: WeightUnit

    var body: some View {
        HStack(spacing: 2) {
            ForEach(WeightUnit.allCases, id: \.self) { u in
                Button {
                    unit = u
                } label: {
                    Text(u.rawValue)
                        .font(.footStrong)
                        .foregroundStyle(unit == u ? Color.bg : Color.tx2)
                        .frame(width: 36, height: 26)
                        .background(unit == u ? Color.tx : Color.clear, in: .capsule)
                }
                .buttonStyle(.plain)
                .accessibilityLabel(u == .kg ? "Kilograms" : "Pounds")
                .accessibilityAddTraits(unit == u ? .isSelected : [])
            }
        }
        .padding(3)
        .background(Color.well, in: .capsule)
        .overlay(Capsule().strokeBorder(Color.line))
        .animation(.snappy(duration: 0.18), value: unit)
        .sensoryFeedback(.selection, trigger: unit)
    }
}
