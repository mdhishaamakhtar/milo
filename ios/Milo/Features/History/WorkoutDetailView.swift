import SwiftUI

/// A finished workout: the numbers, each exercise's sets, and everything you can do with it.
struct WorkoutDetailView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    let workoutID: UUID
    @State private var data = Live(DetailData())
    @State private var editing = false
    @State private var deleting: WorkoutSummary?
    @State private var renaming = false
    @State private var newName = ""
    @State private var templateDraft: Template?
    @State private var note = ""
    @FocusState private var noteFocused: Bool

    struct DetailData: Sendable {
        var detail: WorkoutDetail?
        var insights: [UUID: ExerciseInsight] = [:]
        var display: WeightUnit = .kg
    }

    var body: some View {
        Group {
            if let d = data.value.detail {
                content(d)
            } else {
                Color.bg.ignoresSafeArea()
            }
        }
        .miloScreen()
        .navigationBarTitleDisplayMode(.inline)
        .onAppear {
            let db = app.db, id = workoutID
            data.watch(db) { d in
                guard let detail = try db.workoutDetail(d, id: id) else { return DetailData() }
                return DetailData(detail: detail, insights: try db.insights(d, for: detail), display: try db.settings(d).displayUnit)
            }
        }
        .onChange(of: data.value.detail?.workout.notes, initial: true) { _, notes in
            if !noteFocused { note = notes ?? "" }
        }
        .onChange(of: data.value.detail?.workout.name) { _, name in
            if let w = data.value.detail?.workout, let name {
                app.miloContext = .workout(w.id, name: name, day: Format.day(w.startedAt))
            }
        }
    }

    private func content(_ d: WorkoutDetail) -> some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                VStack(alignment: .leading, spacing: 4) {
                    Text(d.workout.name).font(.title).tracking(-0.4)
                    Text(timeRange(d.workout)).font(.sub).foregroundStyle(Color.tx2)
                }
                .padding(.horizontal, 4)

                WorkoutStats(minutes: minutes(d.workout), volumeKg: d.volumeKg, sets: d.doneSetCount)

                ScrollView(.horizontal) {
                    HStack(spacing: 8) {
                        Button { app.startWorkout(copying: d.workout.id) } label: { Label("Do it again", systemImage: "play.fill") }
                        Button { templateDraft = draft(d) } label: { Label("Save as template", systemImage: "line.3.horizontal") }
                        Button { editing = true } label: { Label("Edit", systemImage: "pencil") }
                    }
                    .buttonStyle(.chip)
                    .labelStyle(ChipLabelStyle())
                }
                .scrollIndicators(.hidden)

                ForEach(d.entries.filter { !$0.doneSets.isEmpty }) { e in
                    ExerciseSetsCard(entry: e, insight: data.value.insights[e.id], display: data.value.display)
                }

                noteField(d.workout)
            }
            .padding(.horizontal, 16)
            .padding(.top, 4)
            .padding(.bottom, 24)
        }
        .scrollDismissesKeyboard(.interactively)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                ShareLink(item: shareText(d)) { Image(systemName: "square.and.arrow.up") }
                    .accessibilityLabel("Share workout")
            }
            ToolbarSpacer(.fixed, placement: .topBarTrailing)
            ToolbarItem(placement: .topBarTrailing) {
                Menu {
                    Button("Edit sets", systemImage: "pencil") { editing = true }
                    Button("Rename", systemImage: "character.cursor.ibeam") { newName = d.workout.name; renaming = true }
                    Button("Do it again", systemImage: "play") { app.startWorkout(copying: d.workout.id) }
                    Button("Save as template", systemImage: "line.3.horizontal") { templateDraft = draft(d) }
                    Divider()
                    Button("Delete workout", systemImage: "trash", role: .destructive) { deleting = summary(d) }
                } label: {
                    Image(systemName: "ellipsis")
                }
                .accessibilityLabel("More")
            }
        }
        .fullScreenCover(isPresented: $editing) { WorkoutView(workoutID: d.workout.id) }
        .sheet(item: $templateDraft) { TemplateEditor(template: $0) }
        .deleteWorkoutDialog($deleting) { dismiss() }
        .alert("Rename workout", isPresented: $renaming) {
            TextField("Name", text: $newName)
            Button("Save") { rename(d.workout) }
            Button("Cancel", role: .cancel) {}
        }
    }

    private func noteField(_ w: Workout) -> some View {
        HStack(alignment: .top, spacing: 12) {
            Image(systemName: "line.3.horizontal").foregroundStyle(Color.tx3).padding(.top, 3)
            TextField("Add a note about this workout", text: $note, axis: .vertical)
                .font(.body16)
                .focused($noteFocused)
                .onChange(of: noteFocused) { _, focused in if !focused { saveNote(w) } }
                .onSubmit { saveNote(w) }
        }
        .padding(16)
        .background(Color.s1, in: .rect(cornerRadius: 20))
        .overlay(RoundedRectangle(cornerRadius: 20).strokeBorder(Color.line))
    }

    private func saveNote(_ w: Workout) {
        guard note != w.notes else { return }
        var updated = w
        updated.notes = note
        try? app.db.save(updated)
        app.edited()
    }

    private func rename(_ w: Workout) {
        let name = newName.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !name.isEmpty, name != w.name else { return }
        var updated = w
        updated.name = name
        try? app.db.save(updated)
        app.edited()
    }

    private func draft(_ d: WorkoutDetail) -> Template {
        Template(
            id: UUID(), name: d.workout.name, notes: "",
            items: d.entries.map { TemplateItem(exerciseId: $0.exercise.id, groupLabel: $0.we.groupLabel) },
            updatedAt: .now, deleted: false, dirty: true)
    }

    private func summary(_ d: WorkoutDetail) -> WorkoutSummary {
        WorkoutSummary(workout: d.workout, exerciseNames: d.entries.map(\.exercise.name), doneSets: d.doneSetCount, volumeKg: d.volumeKg)
    }

    private func minutes(_ w: Workout) -> Int {
        max(1, Int(((w.endedAt ?? .now).timeIntervalSince(w.startedAt) / 60).rounded()))
    }

    /// "Saturday 7 November, 07:51 to 08:35", in the phone's 12- or 24-hour clock.
    private func timeRange(_ w: Workout) -> String {
        let end = w.endedAt.map { " to " + $0.formatted(date: .omitted, time: .shortened) } ?? ""
        return Format.longDay(w.startedAt) + ", " + w.startedAt.formatted(date: .omitted, time: .shortened) + end
    }

    private func shareText(_ d: WorkoutDetail) -> String {
        var lines = ["\(d.workout.name), \(Format.longDay(d.workout.startedAt))",
                     "\(minutes(d.workout)) min, \(Format.mass(d.volumeKg, data.value.display)), \(d.doneSetCount) sets"]
        for e in d.entries where !e.doneSets.isEmpty {
            lines.append("")
            lines.append(e.exercise.name)
            lines.append(Format.setsLine(e.doneSets, e.exercise.measurement))
        }
        if !d.workout.notes.isEmpty { lines += ["", d.workout.notes] }
        return lines.joined(separator: "\n")
    }
}

/// Duration, volume and sets in one card.
struct WorkoutStats: View {
    let minutes: Int
    let volumeKg: Double
    let sets: Int

    var body: some View {
        HStack(spacing: 0) {
            stat("\(minutes)", "min", "Duration")
            Divider().overlay(Color.line).padding(.vertical, 4)
            stat(Format.mass(volumeKg, .kg).replacingOccurrences(of: " kg", with: ""), "kg", "Volume").padding(.leading, 16)
            Divider().overlay(Color.line).padding(.vertical, 4)
            stat("\(sets)", nil, "Sets").padding(.leading, 16)
        }
        .fixedSize(horizontal: false, vertical: true)
        .card()
    }

    private func stat(_ value: String, _ unit: String?, _ label: String) -> some View {
        VStack(alignment: .leading, spacing: 1) {
            HStack(alignment: .firstTextBaseline, spacing: 3) {
                Text(value).font(.sofia(23, .bold))
                if let unit { Text(unit).font(.sofia(14, .semibold)).foregroundStyle(Color.tx2) }
            }
            Text(label).font(.cap).foregroundStyle(Color.tx3)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }
}

/// One exercise of a past workout: its done sets, with a new best or an earlier top set noted.
private struct ExerciseSetsCard: View {
    let entry: ExerciseEntry
    let insight: ExerciseInsight?
    let display: WeightUnit

    private var m: MeasurementKind { entry.exercise.measurement }
    /// Logged in the other unit, so each set also shows in the display unit.
    private var otherUnit: Bool { entry.doneSets.contains { !$0.weight.isEmpty && $0.unit != display } }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            NavigationLink(value: ExerciseRoute(id: entry.exercise.id)) {
                HStack {
                    VStack(alignment: .leading, spacing: 2) {
                        Text(entry.exercise.name).font(.h3)
                        Text(subtitle).font(.sub).foregroundStyle(Color.tx2)
                    }
                    Spacer()
                    Image(systemName: "chevron.right").font(.system(size: 13, weight: .semibold)).foregroundStyle(Color.tx3)
                }
                .contentShape(.rect)
            }
            .buttonStyle(.plain)
            .padding(.bottom, 10)

            ForEach(Array(entry.doneSets.enumerated()), id: \.element.id) { i, s in
                HStack(spacing: 14) {
                    Text("\(i + 1)").font(.sofia(14, .semibold)).foregroundStyle(Color.tx3).frame(width: 16, alignment: .leading)
                    Text(Format.set(s, m)).font(.sofia(16, .semibold))
                    if otherUnit, let kg = Maths.toKg(s.weight, s.unit), s.unit != display {
                        Text("\(Format.kgIn(kg, display)) \(display.rawValue)").font(.sofia(14, .regular)).foregroundStyle(Color.tx3)
                    }
                    Spacer(minLength: 4)
                    if insight?.heaviestSet == s.id { Badge(text: "Heaviest yet", color: .bl) }
                }
                .frame(minHeight: 34)
            }

            if let top = insight?.earlierTop, let date = insight?.earlierTopDate {
                Text("Top set on \(Format.shortDay(date)) was \(Format.set(top, m)).")
                    .font(.sub).foregroundStyle(Color.tx2).padding(.top, 8)
            }
            if !entry.we.notes.isEmpty {
                Text(entry.we.notes).font(.sub).foregroundStyle(Color.tx2).padding(.top, 8)
            }
        }
        .card()
    }

    private var subtitle: String {
        let equipment = entry.exercise.equipmentLabel
        guard otherUnit else { return equipment }
        return equipment + (display == .kg ? ", logged in pounds" : ", logged in kilograms")
    }
}
