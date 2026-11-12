import SwiftUI

/// Every finished workout: a month calendar, then the list by month. The Ask bar searches
/// here: it opens into a field, typing finds a workout by its name or an exercise in it,
/// and sending asks Milo.
struct HistoryView: View {
    @Environment(AppModel.self) private var app
    @State private var workouts = Live<[WorkoutSummary]>([])
    @State private var month = MonthCalendar.monthStart(.now)
    @State private var query = ""
    @FocusState private var searchFocused: Bool

    private var search: String { query.trimmingCharacters(in: .whitespacesAndNewlines) }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                TitleLine(text: eyebrow)
                if workouts.value.isEmpty {
                    Text("Finished workouts show up here, with a calendar of the days you trained.")
                        .font(.sub).foregroundStyle(Color.tx2)
                        .frame(maxWidth: .infinity, alignment: .leading)
                        .card()
                } else {
                    if search.isEmpty { MonthCalendar(month: $month, workouts: workouts.value) }
                    ForEach(groups, id: \.title) { group in
                        SectionHeader(group.title)
                        WorkoutList(workouts: group.workouts)
                    }
                    if !search.isEmpty && groups.isEmpty {
                        Text("No workouts match \u{201C}\(search)\u{201D}.")
                            .font(.sub).foregroundStyle(Color.tx2).padding(.horizontal, 4)
                    }
                }
                if !search.isEmpty { AskMiloRow(text: search, ask: ask).padding(.top, 16) }
            }
            .padding(.horizontal, 16)
            .padding(.bottom, 24)
        }
        .scrollDismissesKeyboard(.interactively)
        .refreshable { await app.sync.sync() }
        .miloScreen()
        .tabTitle("History")
        // While searching, the field takes the tab bar's place: on the keyboard, or on the
        // bottom edge once the keyboard is away and the results are being read.
        .toolbar(app.searchingHistory ? .hidden : .automatic, for: .tabBar)
        .safeAreaInset(edge: .bottom) {
            if app.searchingHistory {
                SearchField(text: $query, focused: $searchFocused, ask: ask, close: closeSearch)
                    .transition(.move(edge: .bottom).combined(with: .opacity))
            }
        }
        // Put away with nothing typed, the search is over.
        .onChange(of: searchFocused) { _, focused in if !focused && search.isEmpty { closeSearch() } }
        .onAppear {
            app.miloContext = .history
            let db = app.db
            workouts.watch(db) { try db.finishedWorkouts($0) }
        }
    }

    private func ask() {
        let text = search
        closeSearch()
        app.ask(text)
    }

    private func closeSearch() {
        searchFocused = false
        query = ""
        withAnimation(.settle) { app.searchingHistory = false }
    }

    /// "67 workouts since Sep 2024".
    private var eyebrow: String {
        let all = workouts.value
        guard let first = all.last else { return "No workouts yet" }
        let n = all.count == 1 ? "1 workout" : "\(all.count) workouts"
        return "\(n) since \(first.workout.startedAt.formatted(.dateTime.month(.abbreviated).year()))"
    }

    private var groups: [(title: String, workouts: [WorkoutSummary])] {
        let q = search.lowercased()
        let shown = q.isEmpty ? workouts.value : workouts.value.filter { w in
            w.workout.name.lowercased().contains(q) || w.exerciseNames.contains { $0.lowercased().contains(q) }
        }
        var out: [(title: String, workouts: [WorkoutSummary])] = []
        for w in shown {
            let title = Self.monthTitle(w.workout.startedAt)
            if out.last?.title == title { out[out.count - 1].workouts.append(w) } else { out.append((title, [w])) }
        }
        return out
    }

    /// "November", or "November 2025" for another year.
    private static func monthTitle(_ date: Date) -> String {
        Calendar.current.isDate(date, equalTo: .now, toGranularity: .year)
            ? date.formatted(.dateTime.month(.wide))
            : date.formatted(.dateTime.month(.wide).year())
    }
}

/// History's Ask bar opened into a field: typing searches the list, the arrow asks Milo,
/// and the ✕ beside it ends the search.
private struct SearchField: View {
    @Binding var text: String
    var focused: FocusState<Bool>.Binding
    let ask: () -> Void
    let close: () -> Void

    private var empty: Bool { text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }

    var body: some View {
        GlassEffectContainer(spacing: 8) {
            HStack(spacing: 8) {
                HStack(spacing: 11) {
                    HStack(alignment: .top, spacing: 11) {
                        BarMLogo(size: 18).onFirstLine(of: .sofia(16.5, .regular)).allowsHitTesting(false)
                        TextField("Search workouts, or ask Milo", text: $text, axis: .vertical)
                            .font(.sofia(16.5, .regular))
                            .lineLimit(1...4)
                            .focused(focused)
                            .submitLabel(.search)
                            // The results are already showing: Search just puts the keyboard away.
                            .onReturn(in: $text) { focused.wrappedValue = false }
                    }
                    MicButton(text: $text)
                    if !empty {
                        Button(action: ask) {
                            Image(systemName: "arrow.up").font(.system(size: 15, weight: .bold)).foregroundStyle(Color.bg)
                                .frame(width: 38, height: 38).background(Color.tx, in: .circle)
                        }
                        .buttonStyle(.row)
                        .accessibilityLabel("Ask Milo")
                    }
                }
                .padding(.leading, 17)
                .padding(.trailing, 7)
                .padding(.vertical, 7)
                .focusesOnTap(focused)
                .glassEffect(.regular.interactive(), in: .rect(cornerRadius: 26))
                .animation(.quick, value: empty)

                Button(action: close) {
                    Image(systemName: "xmark").font(.system(size: 16, weight: .semibold)).foregroundStyle(Color.tx)
                        .frame(width: 52, height: 52)
                        .contentShape(.circle)
                }
                .buttonStyle(.plain)
                .glassEffect(.regular.interactive(), in: .circle)
                .accessibilityLabel("Close search")
            }
        }
        .padding(.horizontal, 14)
        .padding(.bottom, 6)
        .onAppear { focused.wrappedValue = true }
    }
}

/// Under the results: what was typed, handed to Milo to answer from the whole log.
private struct AskMiloRow: View {
    let text: String
    let ask: () -> Void

    var body: some View {
        Button(action: ask) {
            HStack(alignment: .firstTextBaseline, spacing: 12) {
                BarMLogo(size: 18)
                VStack(alignment: .leading, spacing: Space.rowText) {
                    Text("Ask Milo").font(.sofia(16, .semibold))
                    Text("\u{201C}\(text)\u{201D}").font(.sofia(14, .regular)).foregroundStyle(Color.tx2).lineLimit(2)
                }
                Spacer(minLength: 0)
            }
            .padding(16)
            .background(Color.s1, in: .rect(cornerRadius: 20))
            .overlay(RoundedRectangle(cornerRadius: 20).strokeBorder(Color.line))
        }
        .buttonStyle(.row)
    }
}

/// One month, Monday first. Trained days are orange and open that workout; today is ringed.
struct MonthCalendar: View {
    @Binding var month: Date
    let workouts: [WorkoutSummary]

    private static var calendar: Calendar {
        var c = Calendar.current
        c.firstWeekday = 2
        return c
    }

    static func monthStart(_ date: Date) -> Date {
        calendar.dateInterval(of: .month, for: date)!.start
    }

    private var cal: Calendar { Self.calendar }
    private var isCurrentMonth: Bool { cal.isDate(month, equalTo: .now, toGranularity: .month) }
    private var earliest: Date { workouts.last.map { Self.monthStart($0.workout.startedAt) } ?? month }

    var body: some View {
        VStack(spacing: 12) {
            HStack {
                Text(month.formatted(.dateTime.month(.wide).year())).font(.h3)
                Spacer()
                step(-1, "chevron.left", "Previous month").disabled(month <= earliest)
                step(1, "chevron.right", "Next month").disabled(isCurrentMonth)
            }
            let columns = Array(repeating: GridItem(.flexible(), spacing: 0), count: 7)
            LazyVGrid(columns: columns, spacing: 4) {
                ForEach(Array(["M", "T", "W", "T", "F", "S", "S"].enumerated()), id: \.offset) { _, d in
                    Text(d).font(.sofia(12, .semibold)).foregroundStyle(Color.tx3)
                }
                ForEach(0..<leadingBlanks, id: \.self) { _ in Color.clear.frame(height: 36) }
                ForEach(days, id: \.self) { day in cell(day) }
            }
        }
        .card(padding: 16)
        .gesture(DragGesture(minimumDistance: 30).onEnded { g in
            if g.translation.width < -40, !isCurrentMonth { move(1) }
            if g.translation.width > 40, month > earliest { move(-1) }
        })
    }

    private func step(_ by: Int, _ icon: String, _ label: String) -> some View {
        Button { move(by) } label: {
            Image(systemName: icon).font(.system(size: 13, weight: .bold)).frame(width: 34, height: 34)
                .background(Color.s2, in: .circle)
        }
        .buttonStyle(.row)
        .accessibilityLabel(label)
    }

    private func move(_ by: Int) {
        withAnimation(.snappy) { month = cal.date(byAdding: .month, value: by, to: month)! }
    }

    private var days: [Date] {
        let range = cal.range(of: .day, in: .month, for: month)!
        return range.map { cal.date(byAdding: .day, value: $0 - 1, to: month)! }
    }

    private var leadingBlanks: Int {
        (cal.component(.weekday, from: month) - cal.firstWeekday + 7) % 7
    }

    @ViewBuilder private func cell(_ day: Date) -> some View {
        let trained = workouts.first { cal.isDate($0.workout.startedAt, inSameDayAs: day) }
        let isToday = cal.isDateInToday(day)
        let future = day > .now
        let number = Text(day.formatted(.dateTime.day()))
            .font(.sofia(15.5, trained != nil || isToday ? .bold : .medium))
            .frame(width: 36, height: 36)
        if let trained {
            NavigationLink(value: WorkoutRoute(id: trained.workout.id)) {
                number.foregroundStyle(Color.or)
                    .background(Color.or.opacity(0.2), in: .circle)
                    .overlay { if isToday { Circle().strokeBorder(Color.tx, lineWidth: 1.5) } }
            }
            .buttonStyle(.row)
            .accessibilityLabel("\(Format.longDay(day)), \(trained.workout.name)")
        } else {
            number.foregroundStyle(isToday ? Color.tx : future ? Color.tx3 : Color.tx2)
                .overlay { if isToday { Circle().strokeBorder(Color.tx, lineWidth: 1.5) } }
                .accessibilityLabel(Format.longDay(day))
        }
    }
}
