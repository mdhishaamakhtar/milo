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
            .font(compact ? .buttonSmall : .button)
            .foregroundStyle(Color.bg)
            .frame(maxWidth: .infinity, minHeight: compact ? 46 : 54)
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
            .font(compact ? .sofia(14, .semibold) : .button)
            .foregroundStyle(Color.tx)
            .padding(.horizontal, compact ? 13 : 18)
            .frame(minHeight: compact ? 34 : 54)
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
            Text(title).font(.h2)
            Spacer()
            trailing.font(.link).foregroundStyle(Color.or).tint(.or)
        }
        .padding(.horizontal, 4)
        .padding(.top, 30)
        .padding(.bottom, 12)
    }
}

/// Small rounded label: "Approximate", "Proposed", "Avg 2.3".
struct Badge: View {
    let text: String
    var color: Color = .or

    var body: some View {
        Text(text)
            .font(.sofia(12, .bold))
            .foregroundStyle(color)
            .padding(.horizontal, 7)
            .frame(height: 22)
            .background(color.opacity(0.14), in: .rect(cornerRadius: 7))
    }
}

/// Milo's mark: four bars spelling an M that ends on the tallest.
struct BarMLogo: View {
    var size: CGFloat = 16
    var color: Color = .or

    var body: some View {
        // The canvas mark: bars of 26, 14, 20 and 36 in a 48-unit box.
        HStack(alignment: .bottom, spacing: size * 3 / 48) {
            ForEach(Array([26.0, 14, 20, 36].enumerated()), id: \.offset) { _, h in
                RoundedRectangle(cornerRadius: size * 2.5 / 48)
                    .fill(color)
                    .frame(width: size * 7.5 / 48, height: size * h / 48)
            }
        }
        .frame(width: size, height: size * 36 / 48, alignment: .bottom)
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
                .font(.sofia(16, .bold))
                .frame(width: 44, height: 44)
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
                        .font(.sofia(12.5, .bold))
                        .foregroundStyle(unit == u ? Color.bg : Color.tx3)
                        .frame(width: 34, height: 22)
                        .background(unit == u ? Color.tx : Color.clear, in: .capsule)
                }
                .buttonStyle(.plain)
                .accessibilityLabel(u == .kg ? "Kilograms" : "Pounds")
                .accessibilityAddTraits(unit == u ? .isSelected : [])
            }
        }
        .padding(2)
        .background(Color.well, in: .capsule)
        .overlay(Capsule().strokeBorder(Color.line2))
        .animation(.snappy(duration: 0.18), value: unit)
        .sensoryFeedback(.selection, trigger: unit)
    }
}

/// Milo's grouped form: the screen background with card-coloured rows, never the
/// system greys.
struct MiloForm<Content: View>: View {
    @ViewBuilder var content: Content

    var body: some View {
        Form {
            Group { content }
                .listRowBackground(Color.s1)
                .listRowSeparatorTint(Color.line)
        }
        .font(.body16)
        .scrollContentBackground(.hidden)
        .miloScreen()
    }
}

/// Cancel and Save for a sheet, the way the canvas draws them: a quiet Cancel and an
/// off-white Save.
struct SheetToolbar: ToolbarContent {
    var saveTitle = "Save"
    var canSave = true
    let cancel: () -> Void
    let save: () -> Void

    var body: some ToolbarContent {
        ToolbarItem(placement: .cancellationAction) {
            Button("Cancel", action: cancel).tint(Color.tx)
        }
        ToolbarItem(placement: .confirmationAction) {
            Button(saveTitle, action: save)
                .buttonStyle(.glassProminent)
                .tint(Color.tx)
                .foregroundStyle(Color.bg)
                .disabled(!canSave)
        }
    }
}
