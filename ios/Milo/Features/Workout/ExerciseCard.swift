import SwiftUI

/// The open exercise: the set table with last time beside today, and its chips.
struct ExerciseCard: View {
    @Environment(WorkoutModel.self) private var model
    let entry: ExerciseEntry
    @State private var noting = false
    @State private var note = ""

    private var m: MeasurementKind { entry.exercise.measurement }
    private var hasWeight: Bool { m == .repsLoad || m == .assisted }
    /// The first set not ticked is the one to do now.
    private var currentSetID: UUID? { entry.sets.first { !$0.done }?.id }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .firstTextBaseline) {
                Text(entry.exercise.shortName).font(.sofia(22, .bold)).tracking(-0.2)
                if entry.exercise.approximate { Badge(text: "Approximate") }
                Spacer()
                Menu {
                    Button("Remove from workout", systemImage: "trash", role: .destructive) { model.askRemove(entry) }
                } label: {
                    Image(systemName: "ellipsis").font(.system(size: 17, weight: .semibold)).foregroundStyle(Color.tx2)
                        .frame(width: 36, height: 30)
                }
            }
            Text(subtitle).font(.sub).foregroundStyle(Color.tx2).padding(.top, 2)

            grid.padding(.top, 18)

            if let tip = unitTip {
                HStack(alignment: .top, spacing: 14) {
                    Text(tip).font(.sofia(15, .bold)).fixedSize()
                    Text("Tap \(display.rawValue) and every number converts. Milo remembers \(entry.unit == .lb ? "pounds" : "kilograms") for this exercise.")
                        .font(.sofia(13.5)).foregroundStyle(Color.tx2)
                }
                .padding(14)
                .frame(maxWidth: .infinity, alignment: .leading)
                .background(Color.well, in: .rect(cornerRadius: 16))
                .overlay(RoundedRectangle(cornerRadius: 16).strokeBorder(Color.line))
                .padding(.top, 14)
            }

            HStack(spacing: 8) {
                Button { model.addSet(to: entry) } label: { Label("Set", systemImage: "plus") }
                Button { noting.toggle(); note = entry.we.notes } label: { Label("Note", systemImage: "line.3.horizontal") }
                if !model.isPast {
                    Menu {
                        ForEach([30, 45, 60, 90, 120, 150, 180], id: \.self) { s in
                            Button(Format.clock(s)) { model.setRest(s, for: entry) }
                        }
                    } label: {
                        Label("Rest \(Format.clock(entry.restS ?? 60))", systemImage: "timer")
                    }
                }
            }
            .buttonStyle(.chip)
            .labelStyle(ChipLabelStyle())
            .padding(.top, 16)

            if noting || !entry.we.notes.isEmpty {
                TextField("A note for this exercise", text: noting ? $note : .constant(entry.we.notes), axis: .vertical)
                    .font(.sub)
                    .padding(12)
                    .background(Color.well, in: .rect(cornerRadius: Radius.well))
                    .padding(.top, 12)
                    .disabled(!noting)
                    .onSubmit { saveNote() }
                    .onChange(of: noting) { _, now in if !now { saveNote() } }
            }
        }
        .padding(18)
        .background(Color.s1, in: .rect(cornerRadius: Radius.card))
        .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
    }

    private var display: WeightUnit { model.settings.value.displayUnit }

    private var subtitle: String {
        let equipment = entry.exercise.equipmentLabel
        let lead = equipment.isEmpty ? "" : "\(equipment). "
        if hasWeight && entry.unit != display { return lead + (entry.unit == .lb ? "Logged in pounds." : "Logged in kilograms.") }
        if let last = entry.lastDate { return lead + "Filled in from \(Format.shortDay(last))." }
        return lead + "First time."
    }

    /// "45 lb = 20.4 kg" for an exercise lifted in the other unit.
    private var unitTip: String? {
        guard hasWeight, entry.unit != display,
              let top = entry.sets.max(by: { (Maths.toKg($0.weight, $0.unit) ?? 0) < (Maths.toKg($1.weight, $1.unit) ?? 0) }),
              !top.weight.isEmpty else { return nil }
        return "\(Format.weight(top, in: entry.unit)) \(entry.unit.rawValue) = \(Format.weight(top, in: display)) \(display.rawValue)"
    }

    private func saveNote() {
        try? model.app.db.setNote(note, for: entry.id)
        model.app.edited()
    }

    // Columns: set number, last time, weight, reps, tick. Rows (not a Grid) so each can swipe.
    private var grid: some View {
        VStack(spacing: 8) {
            HStack(spacing: 8) {
                Text("Set").frame(width: 26)
                Text("Last time").frame(maxWidth: .infinity, alignment: .leading)
                if hasWeight {
                    UnitSwitch(unit: Binding(get: { entry.unit }, set: { model.setUnit($0, for: entry) })).frame(width: 78)
                }
                Text(m == .timed ? "Time" : m == .distance ? "Metres" : "Reps").frame(width: 70)
                Color.clear.frame(width: 40, height: 1)
            }
            .font(.cap)
            .foregroundStyle(Color.tx3)

            ForEach(Array(entry.sets.enumerated()), id: \.element.id) { i, set in
                row(i, set)
                    .swipeToDelete("Delete set") { model.delete(set) }
                    .contextMenu {
                        Button("Delete set", systemImage: "trash", role: .destructive) { model.delete(set) }
                    }
            }
        }
    }

    private func row(_ i: Int, _ set: LoggedSet) -> some View {
        HStack(spacing: 8) {
            Text("\(i + 1)")
                .font(.sofia(15, .bold))
                .foregroundStyle(set.id == currentSetID ? Color.tx : Color.tx3)
                .frame(width: 26)
            Text(lastTime(i))
                .font(.sofia(15, .medium))
                .foregroundStyle(Color.tx3)
                .lineLimit(1)
                .frame(maxWidth: .infinity, alignment: .leading)
            if hasWeight {
                well(set, .weight, text: Format.weight(set, in: entry.unit), unit: entry.unit.rawValue).frame(width: 78)
            }
            switch m {
            case .timed: well(set, .seconds, text: set.durationS.map(String.init) ?? "", unit: "s").frame(width: 70)
            default: well(set, .reps, text: set.reps.map(String.init) ?? "", unit: set.perSide ? "/side" : nil).frame(width: 70)
            }
            Button { model.tick(set, in: entry) } label: {
                tick(set)
            }
            .buttonStyle(.plain)
            .frame(width: 40)
            .accessibilityLabel(set.done ? "Set \(i + 1) done" : "Mark set \(i + 1) done")
        }
        .contentShape(.rect)
    }

    private func lastTime(_ i: Int) -> String {
        guard i < entry.lastTime.count else { return "–" }
        let s = entry.lastTime[i]
        switch m {
        case .repsLoad, .assisted: return "\(Format.weight(s, in: entry.unit).isEmpty ? "–" : Format.weight(s, in: entry.unit)) × \(s.reps ?? 0)"
        default: return Format.set(s, m)
        }
    }

    private func well(_ set: LoggedSet, _ field: WorkoutModel.Editing.Field, text: String, unit: String?) -> some View {
        let editing = model.editing == WorkoutModel.Editing(setID: set.id, field: field)
        let shown = editing ? model.padText : text
        let state: WellState = editing ? .focus : set.done ? .done : set.id == currentSetID ? .current : .ghost
        return Button {
            model.edit(set, field, in: entry)
        } label: {
            HStack(spacing: 2) {
                if !(editing && shown.isEmpty) {
                    Text(shown.isEmpty ? "–" : shown)
                        .font(.sofia(18.5, .bold))
                        .monospacedDigit()
                }
                if editing {
                    RoundedRectangle(cornerRadius: 1).fill(Color.or).frame(width: 2, height: 22)
                }
                if let unit, !shown.isEmpty {
                    Text(unit).font(.sofia(12.5, .semibold)).foregroundStyle(Color.tx3)
                }
            }
            .foregroundStyle(state == .done ? Color.tx2 : state == .ghost ? Color.tx3 : Color.tx)
            .frame(maxWidth: .infinity, minHeight: 42)
            .background(state.fill, in: .rect(cornerRadius: Radius.well))
            .overlay(RoundedRectangle(cornerRadius: Radius.well).strokeBorder(state.stroke, lineWidth: editing ? 1.5 : 1))
            .shadow(color: editing ? Color.or.opacity(0.25) : .clear, radius: 6)
        }
        .buttonStyle(.plain)
    }

    private func tick(_ set: LoggedSet) -> some View {
        ZStack {
            if set.done {
                Circle().fill(Color.or)
                Image(systemName: "checkmark").font(.system(size: 14, weight: .bold)).foregroundStyle(Color(hex: 0x1A0E04))
            } else if set.id == currentSetID {
                Circle().strokeBorder(Color.tx2, lineWidth: 1.5)
                Image(systemName: "checkmark").font(.system(size: 14, weight: .semibold)).foregroundStyle(Color.tx2)
            } else {
                Circle().strokeBorder(Color.s3, lineWidth: 1.5)
            }
        }
        .frame(width: 32, height: 32)
        .animation(.snappy(duration: 0.2), value: set.done)
        .sensoryFeedback(.success, trigger: set.done) { _, now in now }
    }
}

private enum WellState {
    case done, current, ghost, focus

    var fill: Color {
        switch self {
        case .done, .ghost: .clear
        case .current, .focus: .s2
        }
    }

    var stroke: Color {
        switch self {
        case .done: .clear
        case .ghost: .line
        case .current: .line2
        case .focus: .or
        }
    }
}

/// Icon and title close together, for chips.
struct ChipLabelStyle: LabelStyle {
    func makeBody(configuration: Configuration) -> some View {
        HStack(spacing: 6) {
            configuration.icon.font(.system(size: 13, weight: .semibold))
            configuration.title
        }
    }
}
