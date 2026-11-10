import SwiftUI

struct TodayView: View {
    @Environment(AppModel.self) private var app
    @State private var data = Live(TodayData())
    @State private var editingTemplate: Template?

    struct TodayData: Sendable {
        var running: WorkoutDetail?
        var recent: [WorkoutSummary] = []
        var templates: [TemplateSummary] = []
        var lastWeek: [WorkoutSummary] = []
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                ScreenHeader(eyebrow: Format.longDay(.now), title: "Today")
                if let running = data.value.running {
                    LiveWorkoutCard(detail: running) { app.showingWorkout = true }
                } else {
                    WeekCard(workouts: data.value.lastWeek) { app.startWorkout() }
                }

                SectionHeader("Templates") {
                    Button("New") { editingTemplate = Template(id: UUID(), name: "", notes: "", items: [], updatedAt: .now, deleted: false, dirty: true) }
                }
                if data.value.templates.isEmpty {
                    Text("Save a workout as a template, or make one here. A template is just the list of exercises; numbers fill in from last time.")
                        .font(.sub)
                        .foregroundStyle(Color.tx2)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .card()
                } else {
                    ScrollView(.horizontal) {
                        HStack(spacing: 12) {
                            ForEach(data.value.templates) { t in
                                TemplateCard(summary: t, edit: { editingTemplate = t.template }) {
                                    app.startWorkout(template: t.template)
                                }
                            }
                        }
                        .padding(.horizontal, 16)
                    }
                    .scrollIndicators(.hidden)
                    .padding(.horizontal, -16)
                }

                SectionHeader("Recent") {
                    if !data.value.recent.isEmpty {
                        NavigationLink("All history") { HistoryView() }
                    }
                }
                if data.value.recent.isEmpty {
                    Text("Finished workouts show up here.")
                        .font(.sub)
                        .foregroundStyle(Color.tx2)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .card()
                } else {
                    WorkoutList(workouts: data.value.recent)
                }
            }
            .padding(.horizontal, 16)
            .padding(.bottom, 24)
        }
        .miloScreen()
        .toolbarVisibility(.hidden, for: .navigationBar)
        .sheet(item: $editingTemplate) { TemplateEditor(template: $0) }
        .onAppear {
            app.miloContext = .today
            let db = app.db
            data.watch(db) { d in
                let finished = try db.finishedWorkouts(d, limit: 40)
                let weekAgo = Calendar.current.date(byAdding: .day, value: -6, to: Calendar.current.startOfDay(for: .now))!
                return TodayData(
                    running: try db.runningWorkout(d).flatMap { try db.workoutDetail(d, id: $0.id) },
                    recent: Array(finished.prefix(3)),
                    templates: try db.templates(d),
                    lastWeek: finished.filter { $0.workout.startedAt >= weekAgo })
            }
        }
    }
}

/// The last seven days, how many workouts, and Start workout.
private struct WeekCard: View {
    let workouts: [WorkoutSummary]
    let start: () -> Void

    private var days: [Date] {
        let today = Calendar.current.startOfDay(for: .now)
        return (0..<7).reversed().map { Calendar.current.date(byAdding: .day, value: -$0, to: today)! }
    }

    var body: some View {
        VStack(spacing: 0) {
            HStack(spacing: 0) {
                ForEach(days, id: \.self) { day in
                    let trained = workouts.contains { Calendar.current.isDate($0.workout.startedAt, inSameDayAs: day) }
                    let isToday = Calendar.current.isDateInToday(day)
                    VStack(spacing: 8) {
                        Text(day.formatted(.dateTime.weekday(.abbreviated)))
                            .font(.sofia(13, .medium))
                            .foregroundStyle(isToday ? Color.tx : Color.tx2)
                        ZStack {
                            if trained {
                                Circle().fill(Color.or.opacity(0.18))
                                Image(systemName: "checkmark").font(.system(size: 14, weight: .bold)).foregroundStyle(Color.or)
                            } else {
                                Circle().fill(isToday ? Color.clear : Color.s2.opacity(0.6))
                                Text(day.formatted(.dateTime.day())).font(.sofia(16, .semibold)).foregroundStyle(isToday ? Color.tx : Color.tx3)
                            }
                            if isToday {
                                Circle().strokeBorder(Color.tx, lineWidth: 2)
                            }
                        }
                        .frame(width: 40, height: 40)
                    }
                    .frame(maxWidth: .infinity)
                    .accessibilityElement(children: .ignore)
                    .accessibilityLabel(day.formatted(.dateTime.weekday(.wide)) + (trained ? ", trained" : ""))
                }
            }
            Divider().overlay(Color.line).padding(.vertical, 16)
            HStack {
                Text(countText).font(.sofia(15, .semibold))
                Spacer()
                if let last = workouts.first {
                    Text("Last: \(last.workout.name), \(last.workout.startedAt.formatted(.dateTime.weekday(.abbreviated)))")
                        .font(.sofia(14.5, .regular))
                        .foregroundStyle(Color.tx2)
                        .lineLimit(1)
                }
            }
            Button(action: start) {
                Label("Start workout", systemImage: "plus")
            }
            .buttonStyle(.primary)
            .padding(.top, 16)
        }
        .card()
    }

    private var countText: String {
        switch workouts.count {
        case 0: "No workouts in the last 7 days"
        case 1: "1 workout in the last 7 days"
        default: "\(workouts.count) workouts in the last 7 days"
        }
    }
}

/// Today while a workout runs: the clock and a way back in.
private struct LiveWorkoutCard: View {
    let detail: WorkoutDetail
    let resume: () -> Void

    private var current: (ExerciseEntry, Int)? {
        guard let entry = detail.entries.first(where: { !$0.isFinished }) ?? detail.entries.last else { return nil }
        return (entry, min(entry.doneSets.count + 1, max(entry.sets.count, 1)))
    }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(spacing: 7) {
                Circle().fill(Color.or).frame(width: 8, height: 8)
                    .overlay(Circle().stroke(Color.or.opacity(0.3), lineWidth: 4))
                Text("Workout in progress").font(.sofia(13.5, .semibold)).foregroundStyle(Color.or)
            }
            HStack(alignment: .bottom) {
                VStack(alignment: .leading, spacing: 4) {
                    Text(timerInterval: detail.workout.startedAt...Date.distantFuture, countsDown: false)
                        .font(.sofia(44, .bold))
                        .monospacedDigit()
                        .tracking(-0.8)
                    if let (entry, setNumber) = current {
                        Text("\(entry.exercise.shortName), set \(setNumber) of \(max(entry.sets.count, 1))")
                            .font(.sub).foregroundStyle(Color.tx2)
                    }
                }
                Spacer()
                VStack(alignment: .trailing, spacing: 2) {
                    Text("\(detail.doneSetCount) \(detail.doneSetCount == 1 ? "set" : "sets")").font(.sofia(17, .bold))
                    Text("logged so far").font(.cap).foregroundStyle(Color.tx3)
                }
            }
            .padding(.top, 12)
            Button("Back to workout", action: resume)
                .buttonStyle(.primary)
                .padding(.top, 18)
        }
        .padding(18)
        .background(Color.s1, in: .rect(cornerRadius: Radius.card))
        .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.or.opacity(0.35)))
    }
}

private struct TemplateCard: View {
    let summary: TemplateSummary
    let edit: () -> Void
    let start: () -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            Text(summary.template.name).font(.h3).lineLimit(1)
            VStack(alignment: .leading, spacing: 3) {
                ForEach(summary.exerciseNames.prefix(3), id: \.self) { name in
                    Text(name).lineLimit(1)
                }
                if summary.exerciseNames.count > 3 {
                    Text("and \(summary.exerciseNames.count - 3) more").foregroundStyle(Color.tx3)
                }
            }
            .font(.sofia(14.5, .regular))
            .foregroundStyle(Color.tx2)
            .padding(.top, 12)
            Spacer(minLength: 8)
            HStack {
                Text(summary.lastUsed.map { "Last \(Format.shortDay($0))" } ?? "Not used yet")
                    .font(.cap).foregroundStyle(Color.tx3)
                Spacer()
                Button(action: start) {
                    Image(systemName: "play.fill")
                        .font(.system(size: 13))
                        .frame(width: 36, height: 36)
                        .background(Color.s3, in: .circle)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Start \(summary.template.name)")
            }
        }
        .frame(width: 210, height: 172, alignment: .topLeading)
        .card()
        .contentShape(.rect)
        .onTapGesture(perform: edit)
    }
}

/// Finished workouts as rows: date, name, length, volume.
struct WorkoutList: View {
    @Environment(AppModel.self) private var app
    let workouts: [WorkoutSummary]

    var body: some View {
        VStack(spacing: 0) {
            ForEach(Array(workouts.enumerated()), id: \.element.id) { i, w in
                if i > 0 { Divider().overlay(Color.line).padding(.leading, 72) }
                NavigationLink(value: w.workout.id) {
                    HStack(spacing: 12) {
                        VStack(spacing: 0) {
                            Text(w.workout.startedAt.formatted(.dateTime.weekday(.abbreviated))).font(.sofia(12, .semibold)).foregroundStyle(Color.tx3)
                            Text(w.workout.startedAt.formatted(.dateTime.day())).font(.sofia(19, .bold))
                        }
                        .frame(width: 44)
                        VStack(alignment: .leading, spacing: 2) {
                            Text(w.workout.name).font(.h3).lineLimit(1)
                            Text(detail(w)).font(.sub).foregroundStyle(Color.tx2)
                        }
                        Spacer(minLength: 8)
                        Text(Format.mass(w.volumeKg, .kg)).font(.sofia(14.5, .regular)).foregroundStyle(Color.tx2)
                        Image(systemName: "chevron.right").font(.system(size: 13, weight: .semibold)).foregroundStyle(Color.tx3)
                    }
                    .padding(.horizontal, 16)
                    .frame(minHeight: 64)
                    .contentShape(.rect)
                }
                .buttonStyle(.plain)
            }
        }
        .background(Color.s1, in: .rect(cornerRadius: Radius.card))
        .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
        .navigationDestination(for: UUID.self) { WorkoutDetailView(workoutID: $0) }
    }

    private func detail(_ w: WorkoutSummary) -> String {
        let n = "\(w.exerciseCount) \(w.exerciseCount == 1 ? "exercise" : "exercises")"
        return w.minutes.map { "\($0) min, \(n)" } ?? n
    }
}
