import SwiftUI

// Milo's buttons. Each answers a touch: none is silent under the finger.

/// Off-white primary button with dark text, full width.
struct PrimaryButtonStyle: ButtonStyle {
    var compact = false
    /// Red, for a button that deletes.
    var destructive = false
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .font(compact ? .buttonSmall : .button)
            .foregroundStyle(destructive ? Color.white : Color.bg)
            .frame(maxWidth: .infinity, minHeight: compact ? 46 : 54)
            .background((destructive ? Color.red : Color.tx).opacity(configuration.isPressed ? 0.85 : 1), in: .capsule)
            .scaleEffect(configuration.isPressed ? 0.98 : 1)
            .animation(.quick, value: configuration.isPressed)
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
            .animation(.quick, value: configuration.isPressed)
    }
}

/// A number pad key: it dims and dips under the finger.
struct PadKeyStyle: ButtonStyle {
    var fill = Color.white.opacity(0.085)
    func makeBody(configuration: Configuration) -> some View {
        configuration.label
            .background(fill.opacity(configuration.isPressed ? 0.55 : 1), in: .rect(cornerRadius: 14))
            .scaleEffect(configuration.isPressed ? 0.96 : 1)
            .animation(.quick, value: configuration.isPressed)
    }
}

/// A tappable row: it dims under the finger, so a tap is never silent.
struct RowButtonStyle: ButtonStyle {
    func makeBody(configuration: Configuration) -> some View {
        Label(configuration: configuration)
    }

    private struct Label: View {
        let configuration: Configuration
        @Environment(\.isEnabled) private var isEnabled

        var body: some View {
            configuration.label
                .opacity(!isEnabled ? 0.35 : configuration.isPressed ? 0.6 : 1)
                .animation(.quick, value: configuration.isPressed)
        }
    }
}

extension ButtonStyle where Self == RowButtonStyle {
    static var row: RowButtonStyle { RowButtonStyle() }
}

extension ButtonStyle where Self == PrimaryButtonStyle {
    static var primary: PrimaryButtonStyle { PrimaryButtonStyle() }
    static var primaryCompact: PrimaryButtonStyle { PrimaryButtonStyle(compact: true) }
}

extension ButtonStyle where Self == SecondaryButtonStyle {
    static var secondary: SecondaryButtonStyle { SecondaryButtonStyle() }
    static var chip: SecondaryButtonStyle { SecondaryButtonStyle(compact: true) }
}
