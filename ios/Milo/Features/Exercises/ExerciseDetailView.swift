import Charts
import SwiftUI

/// One exercise over time: a chart of the chosen measure, totals, and every session.
/// Your own exercises can be edited or deleted here; any exercise's unit and rest can change.
struct ExerciseDetailView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    let exerciseID: UUID
    @State private var data = Live<ExerciseProgress?>(nil)
    @State private var metric = Metric.top
    /// Where a finger is on the chart; the nearest session is read out.
    @State private var scrubbing: Date?
    @State private var editing = false

    enum Metric: String, CaseIterable { case top = "Top set", volume = "Volume", e1rm = "Est. 1RM" }

    var body: some View {
        Group {
            if let p = data.value { content(p) } else { Color.bg.ignoresSafeArea() }
        }
        .miloScreen()
        .navigationBarTitleDisplayMode(.inline)
        .onAppear {
            let db = app.db, id = exerciseID
            data.watch(db) { try db.exerciseProgress($0, id: id) }
        }
        .onChange(of: data.value?.exercise.id, initial: true) { _, _ in
            if let e = data.value?.exercise { app.miloContext = .exercise(e) }
        }
    }

    private func content(_ p: ExerciseProgress) -> some View {
        let e = p.exercise
        let loaded = e.measurement == .repsLoad || e.measurement == .assisted
        return ScrollView {
            VStack(alignment: .leading, spacing: 14) {
                VStack(alignment: .leading, spacing: Space.headingText) {
                    HStack(alignment: .firstTextBaseline, spacing: 8) {
                        Text(e.shortName).font(.title).tracking(-0.4)
                        if !e.builtin { Badge(text: "Yours", color: .tx2) }
                        if e.approximate { Badge(text: "Approximate") }
                    }
                    Text(subtitle(e)).font(.sub).foregroundStyle(Color.tx2)
                }
                .padding(.horizontal, 4)

                if p.sessions.isEmpty {
                    Text("Not done yet. Add it to a workout and its sets, bests and trend show up here.")
                        .font(.sub).foregroundStyle(Color.tx2)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .card()
                } else {
                    if loaded { MetricPicker(metric: $metric) }
                    chartCard(p, loaded: loaded)
                    totals(p, loaded: loaded)
                    SectionHeader("History").padding(.top, -16)
                    sessionList(p)
                }
            }
            .padding(.horizontal, 16)
            .padding(.top, 4)
            .padding(.bottom, 24)
        }
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                menu(p).tint(Color.tx)
            }
        }
        .sheet(isPresented: $editing) { ExerciseForm(exercise: e) }
    }

    /// "Machine. Chest, triceps, front shoulders."
    private func subtitle(_ e: Exercise) -> String {
        let muscles = Format.muscles(e.primaryMuscles + e.secondaryMuscles)
        return [e.equipmentLabel, muscles].filter { !$0.isEmpty }.joined(separator: ". ") + "."
    }

    // MARK: Chart

    private struct Point: Identifiable {
        var date: Date
        var value: Double
        var session: ExerciseSession
        var id: Date { date }
    }

    private func points(_ p: ExerciseProgress, loaded: Bool) -> [Point] {
        p.sessions.reversed().compactMap { s in
            let value: Double? = if loaded {
                switch metric {
                case .top: s.summary.topLoadKg
                case .volume: s.summary.volumeKg
                case .e1rm: s.summary.e1rmKg
                }
            } else {
                switch p.exercise.measurement {
                case .timed: s.sets.compactMap(\.durationS).max().map(Double.init)
                case .distance: s.sets.compactMap(\.distanceM).max()
                default: s.sets.compactMap(\.reps).max().map(Double.init)
                }
            }
            return value.map { Point(date: s.workout.startedAt, value: $0, session: s) }
        }
    }

    private func chartCard(_ p: ExerciseProgress, loaded: Bool) -> some View {
        let pts = points(p, loaded: loaded)
        let best = pts.max { $0.value < $1.value }
        let scrubbed = scrubbing.flatMap { at in pts.min { abs($0.date.timeIntervalSince(at)) < abs($1.date.timeIntervalSince(at)) } }
        return VStack(alignment: .leading, spacing: 4) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: Space.rowText) {
                    Text(caption(p, loaded: loaded)).font(.cap).foregroundStyle(Color.tx3)
                    if let shown = scrubbed ?? best {
                        headline(shown, p, loaded: loaded).contentTransition(.numericText()).animation(.quick, value: shown.id)
                    }
                }
                Spacer()
                if let scrubbed {
                    Badge(text: Format.shortDay(scrubbed.date), color: .tx2)
                } else if let best, pts.count > 1 {
                    Badge(text: "↑ Best, \(Format.shortDay(best.date))", color: .bl)
                }
            }
            if pts.count > 1 {
                Chart {
                    ForEach(pts) { pt in
                        LineMark(x: .value("Day", pt.date), y: .value("Value", pt.value))
                            .foregroundStyle(Color.or)
                            .lineStyle(StrokeStyle(lineWidth: 2))
                        PointMark(x: .value("Day", pt.date), y: .value("Value", pt.value))
                            .foregroundStyle(Color.or)
                            .symbolSize(22)
                    }
                    if let marked = scrubbed ?? pts.last {
                        PointMark(x: .value("Day", marked.date), y: .value("Value", marked.value))
                            .foregroundStyle(Color.or.opacity(0.3))
                            .symbolSize(160)
                    }
                    if let scrubbed {
                        RuleMark(x: .value("Day", scrubbed.date))
                            .foregroundStyle(Color.line2)
                            .lineStyle(StrokeStyle(lineWidth: 1))
                    }
                }
                .chartXSelection(value: $scrubbing)
                .sensoryFeedback(.selection, trigger: scrubbed?.id)
                .chartYScale(domain: .automatic(includesZero: false))
                .chartYAxis {
                    AxisMarks(values: .automatic(desiredCount: 3)) { _ in AxisGridLine().foregroundStyle(Color.line) }
                }
                .chartXAxis {
                    AxisMarks(values: axisDates(pts)) { value in
                        // The first and last labels sit inside the card rather than centred off its edge.
                        AxisValueLabel(anchor: value.index == 0 ? .topLeading : value.index == value.count - 1 ? .topTrailing : .top) {
                            if let date = value.as(Date.self) { Text(axisLabel(date, pts)) }
                        }
                        .foregroundStyle(Color.tx3)
                        .font(.sofia(12, .medium))
                    }
                }
                .frame(height: 150)
                .padding(.top, 14)
            }
        }
        .card()
    }

    private func caption(_ p: ExerciseProgress, loaded: Bool) -> String {
        guard loaded else {
            switch p.exercise.measurement {
            case .timed: return "Longest set"
            case .distance: return "Farthest"
            default: return "Most reps in a set"
            }
        }
        switch metric {
        case .top: return "Heaviest set"
        case .volume: return "Most volume in a session"
        case .e1rm: return "Estimated one-rep max"
        }
    }

    @ViewBuilder private func headline(_ best: Point, _ p: ExerciseProgress, loaded: Bool) -> some View {
        let unit = p.unit
        HStack(alignment: .firstTextBaseline, spacing: 4) {
            if loaded {
                let shown = metric == .volume ? Format.mass(best.value, unit).replacingOccurrences(of: " \(unit.rawValue)", with: "") : Format.kgIn(best.value, unit)
                Text(shown).font(.sofia(30, .bold))
                Text(unit.rawValue).font(.sofia(18, .bold))
                if metric == .top, let reps = best.session.summary.topReps {
                    Text("× \(reps)").font(.sofia(16, .semibold)).foregroundStyle(Color.tx2)
                }
            } else {
                switch p.exercise.measurement {
                case .timed: Text(Format.duration(Int(best.value))).font(.sofia(30, .bold))
                case .distance: Text(Format.distance(best.value)).font(.sofia(30, .bold))
                default:
                    Text("\(Int(best.value))").font(.sofia(30, .bold))
                    Text("reps").font(.sofia(18, .bold))
                }
            }
        }
    }

    /// "Sep 2024" across months and years; "7 Oct" within a few months.
    private func axisLabel(_ date: Date, _ pts: [Point]) -> String {
        guard let first = pts.first?.date, let last = pts.last?.date else { return "" }
        let short = last.timeIntervalSince(first) < 120 * 86400
        return short ? Format.shortDay(date) : date.formatted(.dateTime.month(.abbreviated).year())
    }

    /// The first, middle and last session dates.
    private func axisDates(_ pts: [Point]) -> [Date] {
        guard let first = pts.first?.date, let last = pts.last?.date else { return [] }
        return pts.count > 2 ? [first, pts[pts.count / 2].date, last] : [first, last]
    }

    // MARK: Totals and sessions

    private func totals(_ p: ExerciseProgress, loaded: Bool) -> some View {
        let sets = p.sessions.reduce(0) { $0 + $1.sets.count }
        let e1rm = p.sessions.compactMap(\.summary.e1rmKg).max()
        return HStack(spacing: 0) {
            total("\(p.sessions.count)", nil, "Sessions")
            Divider().overlay(Color.line).padding(.vertical, 4)
            total("\(sets)", nil, "Sets").padding(.leading, 16)
            if loaded, let e1rm {
                Divider().overlay(Color.line).padding(.vertical, 4)
                total(Format.kgIn(e1rm, p.unit), p.unit.rawValue, "Est. 1RM").padding(.leading, 16)
            }
        }
        .fixedSize(horizontal: false, vertical: true)
        .card()
    }

    private func total(_ value: String, _ unit: String?, _ label: String) -> some View {
        VStack(alignment: .leading, spacing: Space.rowText) {
            HStack(alignment: .firstTextBaseline, spacing: 3) {
                Text(value).font(.sofia(23, .bold))
                if let unit { Text(unit).font(.sofia(14, .semibold)).foregroundStyle(Color.tx2) }
            }
            Text(label).font(.cap).foregroundStyle(Color.tx3)
        }
        .frame(maxWidth: .infinity, alignment: .leading)
    }

    private func sessionList(_ p: ExerciseProgress) -> some View {
        VStack(spacing: 0) {
            ForEach(Array(p.sessions.enumerated()), id: \.element.id) { i, s in
                if i > 0 { Divider().overlay(Color.line).padding(.leading, 72) }
                NavigationLink(value: WorkoutRoute(id: s.workout.id)) {
                    HStack(spacing: 12) {
                        VStack(spacing: 0) {
                            Text(s.workout.startedAt.formatted(.dateTime.month(.abbreviated))).font(.sofia(12, .semibold)).foregroundStyle(Color.tx3)
                            Text(s.workout.startedAt.formatted(.dateTime.day())).font(.sofia(19, .bold))
                        }
                        .frame(width: 44)
                        VStack(alignment: .leading, spacing: Space.rowText) {
                            Text(s.topSet.map { Format.set($0, p.exercise.measurement, unit: p.unit) } ?? "").font(.h3)
                            Text(sessionDetail(s, p)).font(.sub).foregroundStyle(Color.tx2)
                        }
                        Spacer(minLength: 8)
                        Image(systemName: "chevron.right").font(.system(size: 13, weight: .semibold)).foregroundStyle(Color.tx3)
                    }
                    .padding(.horizontal, 16)
                    .frame(minHeight: 64)
                    .contentShape(.rect)
                }
                .buttonStyle(.row)
            }
        }
        .background(Color.s1, in: .rect(cornerRadius: Radius.card))
        .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
    }

    /// "3 sets, 1,350 kg".
    private func sessionDetail(_ s: ExerciseSession, _ p: ExerciseProgress) -> String {
        let n = "\(s.sets.count) \(s.sets.count == 1 ? "set" : "sets")"
        return s.summary.volumeKg > 0 ? "\(n), \(Format.mass(s.summary.volumeKg, p.unit))" : n
    }

    // MARK: Menu

    private func menu(_ p: ExerciseProgress) -> some View {
        Menu {
            if p.exercise.measurement == .repsLoad || p.exercise.measurement == .assisted {
                Picker("Weigh in", selection: Binding(get: { p.unit }, set: { setUnit($0, p) })) {
                    Text("Kilograms").tag(WeightUnit.kg)
                    Text("Pounds").tag(WeightUnit.lb)
                }
                .pickerStyle(.menu)
            }
            Picker("Rest between sets", selection: Binding(get: { p.restS ?? 60 }, set: { setRest($0, p) })) {
                ForEach([30, 45, 60, 90, 120, 150, 180], id: \.self) { Text(Format.clock($0)).tag($0) }
            }
            .pickerStyle(.menu)
            if !p.exercise.builtin {
                Divider()
                Button("Edit exercise", systemImage: "pencil") { editing = true }
                Button("Delete exercise", systemImage: "trash", role: .destructive) {
                    if let rows = app.write("delete the exercise", { try app.db.deleteExercise(p.exercise.id) }) {
                        app.edited()
                        app.offerUndo("Deleted \(p.exercise.name)", rows: rows)
                    }
                    dismiss()
                }
            }
        } label: {
            Label("Exercise settings", systemImage: "ellipsis")
        }
        .accessibilityLabel("Exercise settings")
    }

    private func setUnit(_ unit: WeightUnit, _ p: ExerciseProgress) {
        app.write("change the unit", { try app.db.setUnit(unit, for: p.exercise.id) })
        app.edited()
    }

    private func setRest(_ seconds: Int, _ p: ExerciseProgress) {
        app.write("change the rest timer", { try app.db.setRest(seconds, for: p.exercise.id) })
        app.edited()
    }
}

/// Top set | Volume | Est. 1RM.
private struct MetricPicker: View {
    @Binding var metric: ExerciseDetailView.Metric

    var body: some View {
        HStack(spacing: 4) {
            ForEach(ExerciseDetailView.Metric.allCases, id: \.self) { m in
                Button { withAnimation(.quick) { metric = m } } label: {
                    Text(m.rawValue)
                        .font(.sofia(14.5, .semibold))
                        .foregroundStyle(metric == m ? Color.tx : Color.tx2)
                        .frame(maxWidth: .infinity, minHeight: 34)
                        .background(metric == m ? Color.s3 : Color.clear, in: .rect(cornerRadius: 10))
                }
                .buttonStyle(.row)
                .accessibilityAddTraits(metric == m ? .isSelected : [])
            }
        }
        .padding(4)
        .background(Color.s1, in: .rect(cornerRadius: 14))
        .overlay(RoundedRectangle(cornerRadius: 14).strokeBorder(Color.line))
        .sensoryFeedback(.selection, trigger: metric)
    }
}
