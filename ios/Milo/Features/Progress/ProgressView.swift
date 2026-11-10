import Charts
import SwiftUI

/// The last twelve weeks: how often, what got heavier, the most-done exercises, body weight.
struct ProgressTab: View {
    @Environment(AppModel.self) private var app
    @State private var data = Live(ProgressData())

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                ScreenHeader(eyebrow: "Last 12 weeks", title: "Progress")
                WeeksCard(weeks: data.value.weeks)

                SectionHeader("New bests")
                if data.value.bests.isEmpty {
                    note("When a set beats your heaviest for an exercise, it shows up here.")
                } else {
                    rows(data.value.bests) { best in
                        NavigationLink(value: ExerciseRoute(id: best.exercise.id)) {
                            HStack(spacing: 12) {
                                Image(systemName: "arrow.up").font(.system(size: 13, weight: .bold)).foregroundStyle(Color.bl)
                                    .frame(width: 32, height: 32).background(Color.bl.opacity(0.14), in: .rect(cornerRadius: 9))
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(best.exercise.name).font(.sofia(16, .semibold)).lineLimit(1)
                                    Text("\(best.text), heaviest yet").font(.sofia(14)).foregroundStyle(Color.tx2)
                                }
                                Spacer(minLength: 8)
                                Text(Format.shortDay(best.date)).font(.sofia(14)).foregroundStyle(Color.tx2)
                            }
                            .rowStyle()
                        }
                        .buttonStyle(.row)
                    }
                }

                SectionHeader("Exercises") {
                    NavigationLink(data.value.exerciseCount > 0 ? "All \(data.value.exerciseCount)" : "All") { ExercisesView() }
                }
                if data.value.top.isEmpty {
                    note("Your most-done exercises show up here, with their top sets over time.")
                } else {
                    rows(data.value.top) { t in
                        NavigationLink(value: ExerciseRoute(id: t.exercise.id)) {
                            HStack(spacing: 12) {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(t.exercise.name).font(.sofia(16, .semibold)).lineLimit(1)
                                    Text(trendText(t)).font(.sofia(14)).foregroundStyle(Color.tx2)
                                }
                                Spacer(minLength: 8)
                                if t.points.count > 1 { Sparkline(points: t.points).frame(width: 64, height: 26) }
                                Image(systemName: "chevron.right").font(.system(size: 13, weight: .semibold)).foregroundStyle(Color.tx3)
                            }
                            .rowStyle()
                        }
                        .buttonStyle(.row)
                    }
                }

                SectionHeader("Body")
                NavigationLink { BodyView() } label: { bodyCard }.buttonStyle(.row)
            }
            .padding(.horizontal, 16)
            .padding(.bottom, 24)
        }
        .miloScreen()
        .toolbarVisibility(.hidden, for: .navigationBar)
        .onAppear {
            app.miloContext = .progress
            let db = app.db
            data.watch(db) { try db.progress($0) }
        }
    }

    private var bodyCard: some View {
        HStack {
            VStack(alignment: .leading, spacing: 4) {
                Text("Weight").font(.cap).foregroundStyle(Color.tx3)
                if let b = data.value.body {
                    HStack(alignment: .firstTextBaseline, spacing: 3) {
                        Text(Format.kgIn(b.latestKg, data.value.display)).font(.sofia(26, .bold))
                        Text(data.value.display.rawValue).font(.sofia(15, .semibold)).foregroundStyle(Color.tx2)
                    }
                    if let change = b.changeKg, let since = b.since {
                        Text(changeText(change, since)).font(.sofia(14)).foregroundStyle(Color.tx2)
                    }
                } else {
                    Text("Add your weight").font(.sofia(18, .semibold))
                    Text("Milo uses it for calories and bodyweight exercises.").font(.sofia(14)).foregroundStyle(Color.tx2)
                }
            }
            Spacer()
            Image(systemName: "chevron.right").font(.system(size: 13, weight: .semibold)).foregroundStyle(Color.tx3)
        }
        .card()
        .contentShape(.rect)
    }

    /// "Down 2.5 kg since August".
    private func changeText(_ kg: Double, _ since: Date) -> String {
        let amount = "\(Format.kgIn(abs(kg), data.value.display)) \(data.value.display.rawValue)"
        let when = since.formatted(.dateTime.month(.wide))
        if abs(kg) < 0.05 { return "Same as in \(when)" }
        return "\(kg < 0 ? "Down" : "Up") \(amount) since \(when)"
    }

    /// "Top set 60 kg, 12 sessions".
    private func trendText(_ t: ExerciseTrend) -> String {
        let n = "\(t.sessions) \(t.sessions == 1 ? "session" : "sessions")"
        guard let kg = t.topKg, kg > 0 else { return n }
        return "Top set \(Format.kgIn(kg, t.unit)) \(t.unit.rawValue), \(n)"
    }

    private func note(_ text: String) -> some View {
        Text(text).font(.sub).foregroundStyle(Color.tx2).frame(maxWidth: .infinity, alignment: .leading).card()
    }

    private func rows<Item: Identifiable, Row: View>(_ items: [Item], @ViewBuilder row: @escaping (Item) -> Row) -> some View {
        VStack(spacing: 0) {
            ForEach(Array(items.enumerated()), id: \.element.id) { i, item in
                if i > 0 { Divider().overlay(Color.line).padding(.leading, 16) }
                row(item)
            }
        }
        .background(Color.s1, in: .rect(cornerRadius: Radius.card))
        .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
    }
}

private extension View {
    func rowStyle() -> some View {
        padding(.horizontal, 16).frame(minHeight: 62).contentShape(.rect)
    }
}

/// Workouts a week for twelve weeks, last week picked out.
private struct WeeksCard: View {
    let weeks: [WeekCount]

    /// This week once it has a workout, else last week.
    private var shown: (week: WeekCount, label: String)? {
        if let now = weeks.last, now.count > 0 { return (now, "this week") }
        return weeks.dropLast().last.map { ($0, "last week") }
    }
    private var average: Double { weeks.isEmpty ? 0 : Double(weeks.reduce(0) { $0 + $1.count }) / Double(weeks.count) }
    private var peak: Int { weeks.map(\.count).max() ?? 0 }

    var body: some View {
        VStack(alignment: .leading, spacing: 0) {
            HStack(alignment: .top) {
                VStack(alignment: .leading, spacing: 2) {
                    Text("Workouts a week").font(.cap).foregroundStyle(Color.tx3)
                    HStack(alignment: .firstTextBaseline, spacing: 6) {
                        Text("\(shown?.week.count ?? 0)").font(.sofia(30, .bold))
                        Text(shown?.label ?? "last week").font(.sofia(15, .medium)).foregroundStyle(Color.tx2)
                    }
                }
                Spacer()
                Badge(text: "Avg \(average.formatted(.number.precision(.fractionLength(1))))", color: .tx2)
            }
            Chart(weeks) { w in
                BarMark(
                    x: .value("Week", w.start, unit: .weekOfYear),
                    y: .value("Workouts", w.count > 0 ? Double(w.count) : max(Double(peak), 1) * 0.04),
                    width: .ratio(0.62))
                    .foregroundStyle(w.id == shown?.week.id ? Color.tx : w.count > 0 ? Color.s3 : Color.s2)
                    .clipShape(.rect(cornerRadius: 4))
            }
            .chartYAxis(.hidden)
            .chartXAxis {
                AxisMarks(values: monthStarts) { _ in
                    AxisValueLabel(format: .dateTime.month(.abbreviated), anchor: .topLeading)
                        .foregroundStyle(Color.tx3)
                        .font(.sofia(12, .medium))
                }
            }
            .frame(height: 90)
            .padding(.top, 14)
            .accessibilityLabel("Workouts a week for twelve weeks")

            Divider().overlay(Color.line).padding(.vertical, 14)
            Text(summary).font(.sofia(14.5)).foregroundStyle(Color.tx2)
        }
        .card()
    }

    /// The first week shown of each month, for the month labels.
    private var monthStarts: [Date] {
        var seen = Set<Int>()
        return weeks.compactMap { w in
            let m = Calendar.current.component(.month, from: w.start)
            return seen.insert(m).inserted ? w.start : nil
        }
    }

    private var summary: String {
        let total = weeks.reduce(0) { $0 + $1.count }
        guard total > 0, let busiest = weeks.max(by: { $0.count < $1.count }) else {
            return "No workouts in the last 12 weeks yet."
        }
        let n = total == 1 ? "1 workout" : "\(total) workouts"
        guard busiest.count > 1 else { return "\(n) in 12 weeks." }
        return "\(n) in 12 weeks. The busiest had \(busiest.count), the week of \(Format.shortDay(busiest.start))."
    }
}

/// A small top-set trend line.
private struct Sparkline: View {
    let points: [Double]

    var body: some View {
        Chart(Array(points.enumerated()), id: \.offset) { i, v in
            LineMark(x: .value("Session", i), y: .value("Top", v))
                .foregroundStyle(Color.tx2)
                .lineStyle(StrokeStyle(lineWidth: 1.6, lineCap: .round, lineJoin: .round))
        }
        .chartXAxis(.hidden)
        .chartYAxis(.hidden)
        .chartYScale(domain: .automatic(includesZero: false))
        .accessibilityHidden(true)
    }
}
