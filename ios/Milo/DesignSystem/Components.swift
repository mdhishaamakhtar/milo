import SwiftUI

// Small pieces every screen shares.

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

/// Milo's mark: four bars spelling an M that ends on the tallest. While Milo is
/// working the bars rise and fall in turn.
struct BarMLogo: View {
    var size: CGFloat = 16
    var color: Color = .or
    var working = false
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    // The canvas mark: bars of 26, 14, 20 and 36 in a 48-unit box.
    private static let bars: [CGFloat] = [26, 14, 20, 36]

    var body: some View {
        TimelineView(.animation(paused: !working || reduceMotion)) { context in
            let t = context.date.timeIntervalSinceReferenceDate
            HStack(alignment: .bottom, spacing: size * 3 / 48) {
                ForEach(Array(Self.bars.enumerated()), id: \.offset) { i, h in
                    RoundedRectangle(cornerRadius: size * 2.5 / 48)
                        .fill(color)
                        .frame(width: size * 7.5 / 48, height: size * h / 48)
                        .scaleEffect(y: scale(bar: i, at: t), anchor: .bottom)
                }
            }
        }
        .frame(width: size, height: size * 36 / 48, alignment: .bottom)
        .opacity(working && reduceMotion ? 0.6 : 1)
        .animation(.settle, value: working)
        .accessibilityHidden(true)
    }

    /// Each bar dips to about half and comes back, one after the other.
    private func scale(bar: Int, at t: TimeInterval) -> CGFloat {
        guard working, !reduceMotion else { return 1 }
        return 0.72 + 0.28 * cos(t * 2 * .pi / 1.1 - Double(bar) * 0.9)
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
        .animation(.quick, value: unit)
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
