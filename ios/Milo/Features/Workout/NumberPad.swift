import SwiftUI

/// Milo's own number pad, inline under the set table: steps of 2.5 kg or 5 lb,
/// the unit switch, and Next to move weight → reps → the next set.
struct NumberPad: View {
    @Environment(WorkoutModel.self) private var model

    var body: some View {
        let target = model.editingTarget
        let field = model.editing?.field ?? .weight
        VStack(spacing: 12) {
            HStack {
                Text(prompt(target, field)).font(.sub).foregroundStyle(Color.tx2).lineLimit(1)
                Spacer()
                Button("Done") { model.closePad() }.font(.sofia(16, .semibold)).foregroundStyle(Color.or)
            }
            .padding(.horizontal, 6)
            Grid(horizontalSpacing: 8, verticalSpacing: 8) {
                GridRow { digit("1"); digit("2"); digit("3"); side(stepLabel(+1, field)) { step(+1, field) } }
                GridRow { digit("4"); digit("5"); digit("6"); side(stepLabel(-1, field)) { step(-1, field) } }
                GridRow {
                    digit("7"); digit("8"); digit("9")
                    side("kg ⇄ lb") { model.toggleUnit() }.disabled(field != .weight).opacity(field == .weight ? 1 : 0.4)
                }
                GridRow {
                    digit(".").disabled(field != .weight).opacity(field == .weight ? 1 : 0.4)
                    digit("0")
                    key { model.key("⌫") } label: { Image(systemName: "delete.left").font(.system(size: 20)) }
                    Button { model.next() } label: {
                        Text("Next").font(.sofia(16.5, .bold)).foregroundStyle(Color.bg).frame(width: 92, height: 50)
                    }
                    .buttonStyle(PadKeyStyle(fill: .tx))
                }
            }
        }
        .padding(.horizontal, 10)
        .padding(.top, 14)
        .padding(.bottom, 4)
        .background {
            // A keyboard's material: the list blurs behind it instead of showing through.
            UnevenRoundedRectangle(topLeadingRadius: 26, topTrailingRadius: 26)
                .fill(.regularMaterial)
                .overlay(UnevenRoundedRectangle(topLeadingRadius: 26, topTrailingRadius: 26).fill(Color.s1.opacity(0.7)))
                .overlay(UnevenRoundedRectangle(topLeadingRadius: 26, topTrailingRadius: 26).strokeBorder(Color.line2))
                .ignoresSafeArea(edges: .bottom)
        }
        .sensoryFeedback(.selection, trigger: model.padText)
    }

    private func prompt(_ target: (LoggedSet, ExerciseEntry)?, _ field: WorkoutModel.Editing.Field) -> String {
        guard let (set, entry) = target else { return "" }
        let i = entry.sets.firstIndex { $0.id == set.id } ?? 0
        // The set's name as its row shows it: "Set 2", or "Warm-up".
        let name = set.kind == .normal ? "Set \(SetMark.marks(entry.sets.map(\.kind))[i].text)" : set.kind.title
        let last = i < entry.lastTime.count ? entry.lastTime[i] : nil
        switch field {
        case .weight:
            let lastText = last.map { Format.weight($0, in: entry.unit) }.flatMap { $0.isEmpty ? nil : "\($0) \(entry.unit.rawValue)" }
            return "\(name) weight." + (lastText.map { " Last time \($0)." } ?? "")
        case .reps:
            return "\(name) reps." + (last?.reps.map { " Last time \($0)." } ?? "")
        case .seconds:
            return "\(name) seconds." + (last?.durationS.map { " Last time \($0) s." } ?? "")
        }
    }

    private func stepLabel(_ sign: Double, _ field: WorkoutModel.Editing.Field) -> String {
        let unit = model.editingTarget?.1.unit ?? .kg
        let size = field == .weight ? (unit == .lb ? "5" : "2.5") : "1"
        return (sign > 0 ? "+" : "−") + size
    }

    private func step(_ sign: Double, _ field: WorkoutModel.Editing.Field) {
        if field == .weight {
            model.step(sign)
        } else {
            let value = max(0, (Int(model.padText) ?? 0) + Int(sign))
            model.padText = ""
            model.padFresh = true
            for c in String(value) { model.key(String(c)) }
        }
    }

    private func digit(_ d: String) -> some View {
        key { model.key(d) } label: { Text(d).font(.sofia(23, .medium)) }
    }

    private func side(_ title: String, action: @escaping () -> Void) -> some View {
        Button(action: action) {
            Text(title).font(.sofia(16, .bold)).foregroundStyle(Color.tx).frame(width: 92, height: 50)
        }
        .buttonStyle(PadKeyStyle(fill: .s3))
    }

    private func key<L: View>(action: @escaping () -> Void, @ViewBuilder label: () -> L) -> some View {
        Button(action: action) {
            label().foregroundStyle(Color.tx).frame(maxWidth: .infinity, minHeight: 50)
        }
        .buttonStyle(PadKeyStyle())
    }
}

/// The rest countdown, above the add field.
struct RestBar: View {
    @Environment(WorkoutModel.self) private var model
    let endsAt: Date

    var body: some View {
        TimelineView(.periodic(from: .now, by: 0.25)) { context in
            let left = max(0, endsAt.timeIntervalSince(context.date))
            HStack(spacing: 10) {
                Image(systemName: "timer").font(.system(size: 18, weight: .medium)).foregroundStyle(Color.tx2)
                Text(Format.clock(Int(left.rounded(.up)))).font(.sofia(22, .bold)).monospacedDigit()
                    .contentTransition(.numericText(countsDown: true))
                    .animation(.quick, value: Int(left.rounded(.up)))
                Text(left > 0 ? "rest, then set \(model.restForSet)" : "rest over").font(.sub).foregroundStyle(Color.tx2).lineLimit(1)
                Spacer(minLength: 4)
                Button("+15 s") { model.addRest(15) }.buttonStyle(.chip)
                Button("Skip") { model.skipRest() }.buttonStyle(.chip)
            }
            .padding(.leading, 18)
            .padding(.trailing, 8)
            .frame(height: 56)
            .overlay(alignment: .bottomLeading) {
                GeometryReader { geo in
                    Capsule().fill(Color.or)
                        .frame(width: geo.size.width * (1 - left / max(model.restTotal, 1)), height: 3)
                        .animation(.linear(duration: 0.25), value: left)
                        .frame(maxHeight: .infinity, alignment: .bottom)
                }
                .padding(.horizontal, 20)
                .allowsHitTesting(false)
            }
            .glassEffect(.regular, in: .capsule)
            .onChange(of: left == 0) { _, over in
                if over {
                    UINotificationFeedbackGenerator().notificationOccurred(.success)
                    DispatchQueue.main.asyncAfter(deadline: .now() + 3) { if model.restEndsAt == endsAt { model.skipRest() } }
                }
            }
        }
    }
}
