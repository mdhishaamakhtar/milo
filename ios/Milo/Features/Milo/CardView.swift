import Charts
import GRDB
import MiloAPI
import SwiftProtobuf
import SwiftUI

/// One of Milo's cards. What it shows and which buttons it has depend on its kind and state.
struct CardView: View {
    let card: Milo_Card
    let session: MiloSession
    /// Applied a new exercise to the open workout (the workout screen goes back to its list).
    var onAdded: (() -> Void)?
    /// Open the workout Milo just started.
    var openRunning: (() -> Void)?
    var openWorkout: ((UUID) -> Void)?
    var openExercise: ((UUID) -> Void)?
    var editTemplate: ((UUID) -> Void)?

    @Environment(AppModel.self) private var app
    @State private var editingExercise: Exercise?

    var body: some View {
        let state = session.state(card)
        Group {
            switch (card.kind, state) {
            // A done card always folds; an applied one folds unless it's a chart or a workout.
            case (.done, _),
                 (_, .applied) where card.kind != .chart && card.kind != .workout:
                AppliedBanner(card: card, state: state, open: startsWorkout ? openRunning : nil) { session.undo(card) }
            case (.change, _) where card.lines.count == 1 && card.lines[0].label == "Workout name":
                renameCard(state)
            default:
                fullCard(state)
            }
        }
        .transition(.opacity)
        .animation(.settle, value: state)
        .sheet(item: $editingExercise) { ExerciseForm(exercise: $0) }
        .sensoryFeedback(trigger: state) { _, now in
            switch now {
            case .applied: .success
            case .undone: .impact(weight: .light)
            case .stale: .warning
            default: nil
            }
        }
    }

    /// The card started a workout that is now running.
    private var startsWorkout: Bool {
        card.changes.contains { $0.workout.id == card.workoutID && !$0.workout.hasEndedAt && !$0.workout.deleted && !card.workoutID.isEmpty }
    }

    private func fullCard(_ state: MiloSession.CardState) -> some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 3) {
                    if card.kind == .chart {
                        Text(card.chart.title).font(.cap).foregroundStyle(Color.tx3)
                    } else {
                        Text(card.title).font(.sofia(card.kind == .newExercise ? 20 : 17.5, .bold))
                        if !card.subtitle.isEmpty { Text(card.subtitle).font(.sub).foregroundStyle(Color.tx2) }
                    }
                }
                Spacer(minLength: 8)
                switch card.kind {
                case .newExercise: Badge(text: "Approximate")
                case .change:
                    if state == .stale {
                        Badge(text: "Out of date", color: .tx2)
                    } else if card.destructive {
                        Badge(text: "Delete", color: .red)
                    } else {
                        Badge(text: "Proposed")
                    }
                case .templateDraft: Badge(text: "Draft")
                case .workout:
                    if let id = UUID(protoID: card.workoutID) { Button("Open") { openWorkout?(id) }.buttonStyle(.chip) }
                case .chart:
                    if let id = UUID(protoID: card.exerciseID) { Button("Open") { openExercise?(id) }.buttonStyle(.chip) }
                default: EmptyView()
                }
            }
            if card.kind == .chart { chart.padding(.top, 4) } else { lines.padding(.top, 14) }
            actions(state)
        }
        .padding(16)
        .background(Color.s1, in: .rect(cornerRadius: 22))
        .overlay(RoundedRectangle(cornerRadius: 22).strokeBorder(Color.line))
    }

    @ViewBuilder private var lines: some View {
        switch card.kind {
        case .newExercise:
            VStack(spacing: 0) {
                ForEach(Array(card.lines.filter { !$0.label.hasPrefix("Set ") }.enumerated()), id: \.offset) { _, line in
                    Divider().overlay(Color.line)
                    HStack(alignment: .top) {
                        Text(line.label).font(.sofia(15, .regular)).foregroundStyle(Color.tx2).frame(width: 104, alignment: .leading)
                        Text(line.after).font(.sofia(15.5, .semibold))
                        Spacer(minLength: 0)
                    }
                    .padding(.vertical, 11)
                }
                ForEach(Array(card.lines.filter { $0.label.hasPrefix("Set ") }.enumerated()), id: \.offset) { _, line in
                    HStack {
                        Text(line.label).font(.sofia(14.5, .regular)).foregroundStyle(Color.tx2)
                        Spacer()
                        Text(line.after).font(.sofia(18, .bold))
                    }
                    .padding(.horizontal, 14).frame(minHeight: 50)
                    .background(Color.well, in: .rect(cornerRadius: Radius.well))
                    .overlay(RoundedRectangle(cornerRadius: Radius.well).strokeBorder(Color.line))
                    .padding(.top, 8)
                }
            }
        case .templateDraft:
            VStack(spacing: 0) {
                ForEach(Array(card.lines.enumerated()), id: \.offset) { _, line in
                    Divider().overlay(Color.line)
                    HStack(spacing: 14) {
                        Text(line.label).font(.sofia(14, .semibold)).foregroundStyle(Color.tx3).frame(width: 22, alignment: .leading)
                        Text(line.after).font(.sofia(15.5, .semibold)).lineLimit(1)
                        Spacer(minLength: 6)
                        Text(line.before).font(.sofia(13.5, .regular)).foregroundStyle(Color.tx3)
                    }
                    .frame(minHeight: 42)
                }
            }
        case .workout:
            VStack(spacing: 0) {
                ForEach(Array(card.lines.enumerated()), id: \.offset) { _, line in
                    Divider().overlay(Color.line)
                    HStack {
                        Text(line.label).font(.sofia(15, .semibold)).lineLimit(1)
                        Spacer(minLength: 8)
                        Text(line.after).font(.sofia(14, .regular)).foregroundStyle(Color.tx2).lineLimit(1)
                    }
                    .frame(minHeight: 40)
                }
            }
        default: // change
            VStack(spacing: 0) {
                ForEach(Array(card.lines.enumerated()), id: \.offset) { _, line in
                    Divider().overlay(Color.line)
                    changeLine(line).frame(minHeight: 42)
                }
            }
        }
    }

    private func changeLine(_ line: Milo_CardLine) -> some View {
        HStack(spacing: 8) {
            Text(line.label).font(.sofia(13.5, .medium)).foregroundStyle(Color.tx3).lineLimit(1)
            if line.before.isEmpty {
                Spacer(minLength: 8)
                Text(line.after).font(.sofia(14.5, .regular)).foregroundStyle(Color.tx2)
            } else {
                Text(line.before).font(.sofia(14.5, .regular)).foregroundStyle(Color.tx3).strikethrough(color: Color.tx3)
                Spacer(minLength: 4)
                if !line.label.hasPrefix("Volume") {
                    Image(systemName: "chevron.right").font(.system(size: 11, weight: .semibold)).foregroundStyle(Color.tx3)
                }
                Text(line.after).font(.sofia(15.5, .bold)).foregroundStyle(line.after == "Removed" ? Color.red : Color.tx)
            }
        }
    }

    @ViewBuilder private var chart: some View {
        let points = card.chart.points
        let (big, small) = headline(card.subtitle)
        HStack(alignment: .firstTextBaseline, spacing: 6) {
            Text(big).font(.sofia(26, .bold))
            Text(small).font(.sofia(14.5, .regular)).foregroundStyle(Color.tx2)
        }
        // Body weight is blue, as on the Body screen; lifts are orange.
        let tint = card.chart.metric == "body_weight" ? Color.bl : Color.or
        Chart(Array(points.enumerated()), id: \.offset) { i, p in
            LineMark(x: .value("Date", p.at.date), y: .value("Value", p.value))
                .foregroundStyle(tint)
                .lineStyle(StrokeStyle(lineWidth: 2))
            PointMark(x: .value("Date", p.at.date), y: .value("Value", p.value))
                .foregroundStyle(i == points.count - 1 ? tint : Color.bg)
                .symbolSize(i == points.count - 1 ? 60 : 24)
        }
        .chartYScale(domain: .automatic(includesZero: false))
        .chartYAxis {
            AxisMarks(values: .automatic(desiredCount: 3)) { _ in AxisGridLine().foregroundStyle(Color.line) }
        }
        .chartXAxis {
            AxisMarks(values: .automatic(desiredCount: 3)) { value in
                AxisValueLabel(format: .dateTime.month(.abbreviated).year()).foregroundStyle(Color.tx3)
            }
        }
        .frame(height: 140)
        .padding(.top, 10)
        if !card.chart.caption.isEmpty {
            Text(card.chart.caption).font(.sub).foregroundStyle(Color.tx2).padding(.top, 8)
        }
    }

    /// "60 kg × 7 on 7 Oct" → ("60 kg", "× 7 on 7 Oct").
    private func headline(_ s: String) -> (String, String) {
        if let r = s.range(of: " × ") { return (String(s[..<r.lowerBound]), "×" + s[r.upperBound...]) }
        if let r = s.range(of: " on ") { return (String(s[..<r.lowerBound]), String(s[r.upperBound...])) }
        return (s, "")
    }

    @ViewBuilder private func actions(_ state: MiloSession.CardState) -> some View {
        switch (card.kind, state) {
        case (.newExercise, .pending):
            HStack(spacing: 10) {
                Button(card.workoutID.isEmpty ? "Add exercise" : "Add to workout") {
                    if session.apply(card) { onAdded?() }
                }
                .buttonStyle(.primaryCompact)
                Button("Edit") {
                    // Add it as Milo read it, then open it to correct.
                    guard session.apply(card), let id = UUID(protoID: card.exerciseID) else { return }
                    onAdded?()
                    editingExercise = try? app.db.writer.read { try Exercise.fetchOne($0, key: id) }
                }
                .buttonStyle(.secondary).frame(width: 110)
            }
            .padding(.top, 16)
        case (.change, .pending):
            HStack(spacing: 10) {
                Button(card.applyLabel.isEmpty ? "Apply change" : card.applyLabel) { session.apply(card) }
                    .buttonStyle(PrimaryButtonStyle(compact: true, destructive: card.destructive))
                Button(card.destructive ? "Keep it" : "Not now") { session.dismiss(card) }.buttonStyle(.secondary).frame(width: 120)
            }
            .padding(.top, 16)
        case (.templateDraft, .pending):
            HStack(spacing: 10) {
                Button(card.applyLabel.isEmpty ? "Save template" : card.applyLabel) { session.apply(card) }.buttonStyle(.primaryCompact)
                Button("Edit list") {
                    if session.apply(card), let t = card.changes.first, let id = UUID(protoID: t.template.id) { editTemplate?(id) }
                }
                .buttonStyle(.secondary).frame(width: 120)
            }
            .padding(.top, 16)
        case (_, .stale):
            Text("This changed after Milo looked at it. Ask again for a fresh card.").font(.sub).foregroundStyle(Color.tx2).padding(.top, 12)
        case (_, .dismissed):
            Text(card.destructive ? "Kept." : "Not applied.").font(.sub).foregroundStyle(Color.tx3).padding(.top, 12)
        default:
            EmptyView()
        }
        if !card.note.isEmpty && state == .pending {
            Text(card.note).font(.sub).foregroundStyle(Color.tx2).padding(.top, 12)
        }
    }

    private func renameCard(_ state: MiloSession.CardState) -> some View {
        let line = card.lines[0]
        return HStack {
            VStack(alignment: .leading, spacing: 3) {
                Text("Rename workout").font(.cap).foregroundStyle(Color.tx3)
                Text(line.before).font(.sofia(15, .regular)).foregroundStyle(Color.tx3).strikethrough(color: Color.tx3)
                Text(line.after).font(.sofia(17, .bold))
            }
            Spacer()
            if state == .pending {
                Button("Apply") { session.apply(card) }
                    .font(.sofia(15, .bold)).foregroundStyle(Color.bg)
                    .padding(.horizontal, 18).frame(height: 38).background(Color.tx, in: .capsule)
            }
        }
        .padding(16)
        .background(Color.s1, in: .rect(cornerRadius: 22))
        .overlay(RoundedRectangle(cornerRadius: 22).strokeBorder(Color.line))
    }
}

/// A card once applied: what happened, and Undo.
private struct AppliedBanner: View {
    let card: Milo_Card
    let state: MiloSession.CardState
    var open: (() -> Void)?
    let undo: () -> Void

    /// Under the title: what the card says about itself, then the sets if it logged any.
    private var detail: String {
        let sets = card.kind == .done ? card.lines.filter { $0.label.hasPrefix("Set ") }.map(\.after).joined(separator: ", ") : ""
        return [card.subtitle, sets].filter { !$0.isEmpty }.joined(separator: " · ")
    }

    var body: some View {
        HStack(spacing: 12) {
            ZStack {
                Circle().fill(state == .undone ? Color.s2 : Color.or.opacity(0.18))
                Image(systemName: state == .undone ? "arrow.uturn.backward" : "checkmark")
                    .font(.system(size: 13, weight: .bold)).foregroundStyle(state == .undone ? Color.tx2 : Color.or)
                    .contentTransition(.symbolEffect(.replace))
            }
            .frame(width: 30, height: 30)
            VStack(alignment: .leading, spacing: 2) {
                Text(state == .undone ? "Undone" : card.appliedTitle).font(.sofia(16, .semibold)).lineLimit(2)
                if !detail.isEmpty {
                    Text(detail).font(.sofia(13.5, .regular)).foregroundStyle(Color.tx2).lineLimit(3)
                }
            }
            Spacer(minLength: 6)
            if state == .applied {
                if let open {
                    Button("Open", action: open).buttonStyle(.chip)
                    Button(action: undo) { Image(systemName: "arrow.uturn.backward").font(.system(size: 13, weight: .semibold)) }
                        .buttonStyle(.chip).accessibilityLabel("Undo")
                } else {
                    Button(action: undo) { Label("Undo", systemImage: "arrow.uturn.backward") }
                        .buttonStyle(.chip).labelStyle(ChipLabelStyle())
                }
            }
        }
        .padding(14)
        .background(Color.s1, in: .rect(cornerRadius: 22))
        .overlay(RoundedRectangle(cornerRadius: 22).strokeBorder(Color.line))
    }
}
