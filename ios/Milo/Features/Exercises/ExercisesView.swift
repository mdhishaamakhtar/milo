import SwiftUI

/// Your exercises (done or made) by name, with how many sets you've done. Searching
/// also finds exercises in the built-in library.
struct ExercisesView: View {
    @Environment(AppModel.self) private var app
    @State private var rows = Live<[ExerciseRow]>([])
    @State private var query = ""
    @State private var filter = Filter.all
    @State private var creating = false
    @State private var editing: Exercise?
    @State private var deleting: Exercise?

    enum Filter: String, CaseIterable {
        case all = "All", yours = "Yours", chest = "Chest", back = "Back", legs = "Legs"
        case shoulders = "Shoulders", arms = "Arms", core = "Core"

        /// The library's muscle names each chip covers.
        var muscles: Set<String> {
            switch self {
            case .chest: ["chest"]
            case .back: ["lats", "upper back", "lower back", "traps"]
            case .legs: ["quads", "hamstrings", "glutes", "calves", "adductors", "abductors", "hip flexors"]
            case .shoulders: ["front delts", "side delts", "rear delts"]
            case .arms: ["biceps", "triceps", "forearms"]
            case .core: ["abs", "obliques"]
            default: []
            }
        }
    }

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 12) {
                search
                chips
                ForEach(sections, id: \.title) { section in
                    Text(section.title).font(.cap).foregroundStyle(Color.tx3).padding(.horizontal, 4).padding(.top, 6)
                    VStack(spacing: 0) {
                        ForEach(Array(section.rows.enumerated()), id: \.element.id) { i, row in
                            if i > 0 { Divider().overlay(Color.line).padding(.leading, 16) }
                            rowView(row)
                        }
                    }
                    .background(Color.s1)
                    .clipShape(.rect(cornerRadius: Radius.card))
                    .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
                }
                if sections.isEmpty {
                    Text(query.isEmpty
                        ? "Exercises you do or make show up here. Search to find one in the built-in library."
                        : "No exercise matches \u{201C}\(query)\u{201D}. Tap + to make it, or describe it to Milo below.")
                        .font(.sub).foregroundStyle(Color.tx2).padding(.horizontal, 4)
                }
            }
            .padding(.horizontal, 16)
            .padding(.bottom, 24)
        }
        .scrollDismissesKeyboard(.interactively)
        .miloScreen()
        .navigationTitle("Exercises")
        .navigationBarTitleDisplayMode(.inline)
        .toolbar {
            ToolbarItem(placement: .topBarTrailing) {
                Button { creating = true } label: { Image(systemName: "plus") }.accessibilityLabel("New exercise")
            }
        }
        .sheet(isPresented: $creating) { ExerciseForm(suggestedName: query) }
        .sheet(item: $editing) { ExerciseForm(exercise: $0) }
        .confirmationDialog(
            "Delete \(deleting?.name ?? "exercise")?",
            isPresented: Binding(get: { deleting != nil }, set: { if !$0 { deleting = nil } }),
            titleVisibility: .visible,
            presenting: deleting
        ) { e in
            Button("Delete exercise", role: .destructive) {
                try? app.db.deleteExercise(e.id)
                app.edited()
            }
        } message: { _ in
            Text("It comes off your exercise list. Workouts with it keep it.")
        }
        .onAppear {
            app.miloContext = MiloContext(screen: "exercises", prompt: "Describe an exercise to add it")
            let db = app.db
            rows.watch(db) { try db.exerciseRows($0) }
        }
    }

    private var search: some View {
        HStack(spacing: 10) {
            Image(systemName: "magnifyingglass").foregroundStyle(Color.tx3)
            TextField(mine.isEmpty ? "Search exercises" : "Search \(mine.count) exercises", text: $query)
                .font(.body16)
                .autocorrectionDisabled()
                .submitLabel(.search)
        }
        .padding(.horizontal, 16)
        .frame(height: 46)
        .background(Color.s1, in: .capsule)
        .overlay(Capsule().strokeBorder(Color.line2))
    }

    private var chips: some View {
        ScrollView(.horizontal) {
            HStack(spacing: 8) {
                ForEach(Filter.allCases, id: \.self) { f in
                    Button(f.rawValue) { withAnimation(.snappy(duration: 0.2)) { filter = f } }
                        .font(.sofia(14, .semibold))
                        .foregroundStyle(filter == f ? Color.bg : Color.tx)
                        .padding(.horizontal, 14)
                        .frame(height: 34)
                        .background(filter == f ? Color.tx : Color.s2, in: .capsule)
                        .overlay(Capsule().strokeBorder(filter == f ? Color.clear : Color.line))
                        .buttonStyle(.plain)
                        .accessibilityAddTraits(filter == f ? .isSelected : [])
                }
            }
            .padding(.horizontal, 16)
        }
        .scrollIndicators(.hidden)
        .padding(.horizontal, -16)
        .sensoryFeedback(.selection, trigger: filter)
    }

    @ViewBuilder private func rowView(_ row: ExerciseRow) -> some View {
        let link = NavigationLink(value: ExerciseRoute(id: row.id)) {
            HStack {
                VStack(alignment: .leading, spacing: 2) {
                    HStack(spacing: 6) {
                        Text(row.exercise.name).font(.sofia(16, .semibold)).lineLimit(1)
                        if !row.exercise.builtin { Badge(text: "Yours", color: .or) }
                    }
                    Text(detail(row)).font(.sofia(14)).foregroundStyle(Color.tx2).lineLimit(1)
                }
                Spacer(minLength: 8)
                Image(systemName: "chevron.right").font(.system(size: 13, weight: .semibold)).foregroundStyle(Color.tx3)
            }
            .padding(.horizontal, 16)
            .frame(minHeight: 58)
            .contentShape(.rect)
        }
        .buttonStyle(.plain)
        .background(Color.s1)

        if row.exercise.builtin {
            link
        } else {
            link
                .swipeToDelete("Delete exercise", cornerRadius: 0) { deleting = row.exercise }
                .contextMenu {
                    Button("Edit exercise", systemImage: "pencil") { editing = row.exercise }
                    Button("Delete exercise", systemImage: "trash", role: .destructive) { deleting = row.exercise }
                }
        }
    }

    /// "Shoulders, dumbbell. 16 sets"
    private func detail(_ row: ExerciseRow) -> String {
        let e = row.exercise
        let what = [Format.muscles(Array(e.primaryMuscles.prefix(1))), e.equipment].filter { !$0.isEmpty }.joined(separator: ", ")
        let sets = row.sets > 0 ? "\(row.sets) \(row.sets == 1 ? "set" : "sets")" : ""
        return [what, sets].filter { !$0.isEmpty }.joined(separator: ". ")
    }

    /// Done or made by you.
    private var mine: [ExerciseRow] { rows.value.filter { $0.sets > 0 || !$0.exercise.builtin } }

    /// Yours by letter; when searching, matches from the library follow under their own heading.
    private var sections: [(title: String, rows: [ExerciseRow])] {
        let q = query.trimmingCharacters(in: .whitespaces).lowercased()
        func matches(_ row: ExerciseRow) -> Bool {
            q.isEmpty || ([row.exercise.name] + row.exercise.aliases).contains { $0.lowercased().contains(q) }
        }
        func kept(_ row: ExerciseRow) -> Bool {
            switch filter {
            case .all: true
            case .yours: !row.exercise.builtin
            default: !filter.muscles.isDisjoint(with: row.exercise.primaryMuscles)
            }
        }
        var out: [(title: String, rows: [ExerciseRow])] = []
        for row in mine where matches(row) && kept(row) {
            let letter = row.exercise.name.prefix(1).uppercased()
            if out.last?.title == letter { out[out.count - 1].rows.append(row) } else { out.append((letter, [row])) }
        }
        if !q.isEmpty && filter != .yours {
            let library = rows.value.filter { $0.exercise.builtin && $0.sets == 0 && matches($0) && kept($0) }
            if !library.isEmpty { out.append(("From the library", Array(library.prefix(40)))) }
        }
        return out
    }
}
