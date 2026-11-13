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

/// Milo's mark and name together, the way the app signs itself: on the sign-in screen
/// and at the top of each tab.
struct MiloWordmark: View {
    var working = false

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 9) {
            BarMLogo(size: 22, working: working)
            Text("Milo").font(.sofia(21, .bold, relativeTo: .title3))
        }
        .accessibilityElement(children: .ignore)
        .accessibilityLabel("Milo")
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
        // The bars stand on the line of text beside them: in a row aligned on the text
        // baseline, their bottom meets the bottom of the letters.
        .alignmentGuide(.firstTextBaseline) { $0[.bottom] }
        .alignmentGuide(.lastTextBaseline) { $0[.bottom] }
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

extension BarMLogo {
    /// The mark standing on the first line of a text field set in `font`. A text field
    /// reports its top edge as its baseline, so the mark takes the baseline from an empty
    /// line of the same type instead; put this and the field in a row aligned at the top.
    func onFirstLine(of font: Font) -> some View {
        HStack(alignment: .firstTextBaseline, spacing: 0) {
            self
            Text(verbatim: "\u{200B}").font(font)
        }
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
    /// Set by `cancel` when there are changes to lose; asks before `discard`.
    var discarding: Binding<Bool> = .constant(false)
    var discard: () -> Void = {}

    var body: some ToolbarContent {
        // A round ✕, the way iOS sheets cancel.
        ToolbarItem(placement: .cancellationAction) {
            Button(role: .cancel, action: cancel)
                .tint(Color.tx)
                .confirmationDialog("Discard your changes?", isPresented: discarding, titleVisibility: .visible) {
                    Button("Discard changes", role: .destructive, action: discard)
                    Button("Keep editing", role: .cancel) {}
                }
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

extension View {
    /// The Undo bar for the last delete, floating `above` points over the bottom edge.
    func undoBar(above: CGFloat) -> some View { modifier(UndoBar(above: above)) }

    /// Makes a whole glass field take a tap. A text field only answers on its line of text, and
    /// a thumb at the bottom of the screen often lands on the field's edge instead. Put it after
    /// the field's padding; anything drawn over it that isn't a control needs
    /// `.allowsHitTesting(false)`.
    func focusesOnTap(_ focused: FocusState<Bool>.Binding) -> some View {
        background { Color.clear.contentShape(.rect).onTapGesture { focused.wrappedValue = true } }
    }

    /// Return in a field that grows to several lines (`axis: .vertical`) types a line break and
    /// never calls `onSubmit`: the field gains an empty second line and the text rides up.
    /// This takes that one typed break back out and runs `action`. Pasted text keeps its breaks.
    func onReturn(in text: Binding<String>, perform action: @escaping () -> Void) -> some View {
        onChange(of: text.wrappedValue) { old, new in
            guard returnTyped(old: old, new: new) else { return }
            text.wrappedValue = old
            action()
        }
    }
}

/// `new` is `old` with a single line break typed into it.
func returnTyped(old: String, new: String) -> Bool {
    guard new.count == old.count + 1 else { return false }
    let o = Array(old), n = Array(new)
    var i = 0
    while i < o.count, o[i] == n[i] { i += 1 }
    return n[i] == "\n" && n[(i + 1)...].elementsEqual(o[i...])
}

/// "Deleted Legs, Undo" on glass, for five seconds after a delete. It's a control floating
/// over content, which is where Liquid Glass belongs.
private struct UndoBar: ViewModifier {
    @Environment(AppModel.self) private var app
    let above: CGFloat

    func body(content: Content) -> some View {
        content.overlay(alignment: .bottom) {
            if let undo = app.undo {
                HStack(spacing: 12) {
                    Text(undo.message).font(.sofia(15.5, .medium)).lineLimit(1)
                    Spacer(minLength: 8)
                    if let restore = undo.restore {
                        Button("Undo") {
                            withAnimation(.settle) { app.undo = nil }
                            restore()
                        }
                        .font(.sofia(15.5, .bold))
                        .foregroundStyle(Color.or)
                        .frame(minWidth: 44, minHeight: 44)
                    }
                }
                .padding(.leading, 20)
                .padding(.trailing, 12)
                .frame(height: 52)
                .glassEffect(.regular.interactive(), in: .capsule)
                .padding(.horizontal, 16)
                .padding(.bottom, above)
                .transition(.move(edge: .bottom).combined(with: .opacity))
                .task(id: undo.id) {
                    do {
                        try await Task.sleep(for: .seconds(5))
                        if app.undo?.id == undo.id { withAnimation(.settle) { app.undo = nil } }
                    } catch {}
                }
            }
        }
        .animation(.settle, value: app.undo?.id)
        .sensoryFeedback(.impact(weight: .light), trigger: app.undo?.id) { _, now in now != nil }
    }
}
