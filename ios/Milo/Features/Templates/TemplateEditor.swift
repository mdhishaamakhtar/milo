import GRDB
import OSLog
import SwiftUI

/// A template: a name and a list of exercises. Drag to reorder, swipe to remove,
/// long-press to pair neighbours as a superset, and add from the field at the bottom.
struct TemplateEditor: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var template: Template
    private let original: Template
    @State private var isNew = true
    @State private var rows = Live<[UUID: ExerciseMatch]>([:])
    @State private var suggestions = Live<[Exercise]>([])
    @State private var matches = Live<[ExerciseMatch]>([])
    @State private var query = ""
    @State private var creating: String?
    @State private var confirmingCancel = false
    @State private var deleting = false
    @FocusState private var naming: Bool
    @FocusState private var adding: Bool

    init(template: Template) {
        _template = State(initialValue: template)
        original = template
    }

    private var trimmedName: String { template.name.trimmingCharacters(in: .whitespacesAndNewlines) }
    private var trimmedQuery: String { query.trimmingCharacters(in: .whitespacesAndNewlines) }
    private var hasChanges: Bool { template.name != original.name || template.items != original.items }
    private var labels: [String] { Superset.labels(template.items.map(\.groupLabel)) }

    var body: some View {
        NavigationStack {
            List {
                if trimmedQuery.isEmpty { editing } else { matchSection }
            }
            .listStyle(.insetGrouped)
            .listSectionSpacing(14)
            .scrollContentBackground(.hidden)
            .scrollDismissesKeyboard(.interactively)
            .miloScreen()
            .safeAreaInset(edge: .bottom) { addField }
            .navigationTitle(isNew ? "New template" : "Edit template")
            .navigationBarTitleDisplayMode(.inline)
            .toolbar {
                ToolbarItem(placement: .cancellationAction) {
                    Button("Cancel") { if hasChanges { confirmingCancel = true } else { dismiss() } }
                }
                ToolbarItem(placement: .confirmationAction) {
                    Button("Save", action: save)
                        .buttonStyle(.glassProminent)
                        .tint(Color.tx)
                        .foregroundStyle(Color.bg)
                        .disabled(trimmedName.isEmpty || template.items.isEmpty)
                }
            }
            .confirmationDialog("Discard your changes?", isPresented: $confirmingCancel, titleVisibility: .visible) {
                Button("Discard changes", role: .destructive) { dismiss() }
                Button("Keep editing", role: .cancel) {}
            }
            .confirmationDialog("Delete \(original.name)?", isPresented: $deleting, titleVisibility: .visible) {
                Button("Delete template", role: .destructive) {
                    try? app.db.deleteTemplate(template.id)
                    app.edited()
                    dismiss()
                }
            } message: {
                Text("Workouts you did from it stay in History.")
            }
            .sheet(item: Binding(get: { creating.map(NewName.init) }, set: { creating = $0?.name })) { new in
                ExerciseForm(suggestedName: new.name) { add($0.id) }
            }
        }
        .interactiveDismissDisabled(hasChanges)
        .onAppear {
            let id = template.id
            isNew = (try? app.db.writer.read { try Template.fetchOne($0, key: id) }) == nil
            if isNew && template.name.isEmpty { naming = true }
        }
        .onChange(of: template.items.map(\.exerciseId), initial: true) { _, ids in
            let db = app.db
            rows.watch(db) { d in try Self.rowInfo(d, db, ids) }
            suggestions.watch(db) { try db.companions($0, of: ids) }
        }
        .onChange(of: trimmedQuery) { _, text in
            let db = app.db
            matches.watch(db) { try db.search($0, text, limit: 6) }
        }
    }

    // MARK: The list

    @ViewBuilder private var editing: some View {
        Section {
            TextField("Template name, e.g. Pull day", text: $template.name)
                .font(.sofia(18, .semibold))
                .focused($naming)
                .submitLabel(.done)
                .padding(.horizontal, 16)
                .frame(height: 54)
                .background(Color.s1, in: .rect(cornerRadius: 16))
                .overlay(RoundedRectangle(cornerRadius: 16).strokeBorder(naming ? Color.or : Color.line, lineWidth: naming ? 1.5 : 1))
                .listRowInsets(EdgeInsets())
                .listRowBackground(Color.clear)
        } footer: {
            Text("A template is only the list. Sets and weights fill in from the last time you did each exercise.")
                .font(.sofia(14)).foregroundStyle(Color.tx2)
        }

        Section {
            if template.items.isEmpty {
                Text("No exercises yet. Add them from the field below.")
                    .font(.sub).foregroundStyle(Color.tx2)
                    .listRowBackground(Color.s1)
            }
            ForEach(template.items.indices, id: \.self) { i in
                row(i)
                    .listRowBackground(Color.s1)
                    .listRowSeparatorTint(Color.line)
                    .swipeActions {
                        Button("Remove", systemImage: "trash", role: .destructive) { remove(i) }.tint(Color.red)
                    }
                    .contextMenu { supersetMenu(i) }
            }
            .onMove { from, to in
                template.items.move(fromOffsets: from, toOffset: to)
                tidyGroups()
            }
        }

        if !template.items.isEmpty && !suggestions.value.isEmpty {
            Section {
                ScrollView(.horizontal) {
                    HStack(spacing: 8) {
                        ForEach(suggestions.value) { e in
                            Button { add(e.id) } label: { Label(e.name, systemImage: "plus") }
                        }
                    }
                    .buttonStyle(.chip)
                    .labelStyle(ChipLabelStyle())
                }
                .scrollIndicators(.hidden)
                .listRowInsets(EdgeInsets())
                .listRowBackground(Color.clear)
            } footer: {
                Text("Often done on the same day").font(.cap).foregroundStyle(Color.tx3)
            }
        }

        if !isNew {
            Section {
                Button("Delete template", role: .destructive) { deleting = true }
                    .font(.link)
                    .foregroundStyle(Color.red)
                    .listRowBackground(Color.s1)
            }
        }
    }

    private func row(_ i: Int) -> some View {
        let item = template.items[i]
        let info = rows.value[item.exerciseId]
        let grouped = labels[i].last?.isLetter == true
        return HStack(spacing: 10) {
            Text(labels[i]).font(.sofia(14, .semibold)).foregroundStyle(Color.tx3).frame(width: 22, alignment: .leading)
            VStack(alignment: .leading, spacing: 2) {
                Text(info?.exercise.name ?? "Exercise").font(.sofia(16, .semibold)).lineLimit(1)
                Text(detail(i, info)).font(.sofia(14)).foregroundStyle(Color.tx2).lineLimit(1)
            }
            Spacer(minLength: 8)
            Image(systemName: "line.3.horizontal").font(.system(size: 15, weight: .semibold)).foregroundStyle(Color.tx3)
                .accessibilityHidden(true)
        }
        .overlay(alignment: .leading) {
            if grouped {
                Capsule().fill(Color.or).frame(width: 3).padding(.vertical, -8).offset(x: -12)
            }
        }
    }

    /// "Superset with 4b", or "Last 13 Oct, 90 kg × 8".
    private func detail(_ i: Int, _ info: ExerciseMatch?) -> String {
        let group = template.items[i].groupLabel
        if !group.isEmpty {
            let partners = template.items.indices.filter { $0 != i && template.items[$0].groupLabel == group }.map { labels[$0] }
            if !partners.isEmpty { return "Superset with " + partners.joined(separator: " and ") }
        }
        guard let date = info?.lastDate else { return "Not done yet" }
        return "Last \(Format.shortDay(date))" + (info?.lastText.map { ", \($0)" } ?? "")
    }

    @ViewBuilder private func supersetMenu(_ i: Int) -> some View {
        let group = template.items[i].groupLabel
        if i + 1 < template.items.count && (group.isEmpty || template.items[i + 1].groupLabel != group) {
            Button("Superset with next", systemImage: "link") { pair(i, i + 1) }
        }
        if !group.isEmpty {
            Button("Take out of superset", systemImage: "link.badge.plus") {
                template.items[i].groupLabel = ""
                tidyGroups()
            }
        }
        Button("Remove", systemImage: "trash", role: .destructive) { remove(i) }
    }

    // MARK: Adding

    @ViewBuilder private var matchSection: some View {
        Section {
            ForEach(matches.value) { m in
                Button { add(m.exercise.id) } label: {
                    HStack {
                        VStack(alignment: .leading, spacing: 2) {
                            Text(m.exercise.name).font(.sofia(16, .semibold))
                            Text(m.lastDate.map { "Last \(Format.shortDay($0))" } ?? m.exercise.equipmentLabel)
                                .font(.sofia(14)).foregroundStyle(Color.tx2)
                        }
                        Spacer()
                        Image(systemName: "plus").font(.system(size: 15, weight: .semibold))
                            .frame(width: 34, height: 34).background(Color.s2, in: .circle)
                    }
                    .contentShape(.rect)
                }
                .buttonStyle(.plain)
                .listRowBackground(Color.s1)
            }
            Button { creating = trimmedQuery } label: {
                Label("Create \u{201C}\(trimmedQuery)\u{201D}", systemImage: "plus.circle")
                    .font(.sofia(16, .semibold)).foregroundStyle(Color.or)
            }
            .listRowBackground(Color.s1)
        } header: {
            Text(matches.value.isEmpty ? "Not in your exercises" : "\(matches.value.count) in your exercises")
                .font(.cap).foregroundStyle(Color.tx3)
        }
    }

    private var addField: some View {
        HStack(spacing: 11) {
            Image(systemName: "plus").font(.system(size: 16, weight: .semibold)).foregroundStyle(Color.or)
            TextField("Add an exercise", text: $query)
                .font(.sofia(16.5))
                .focused($adding)
                .submitLabel(.done)
                .autocorrectionDisabled()
            if !query.isEmpty {
                Button { query = "" } label: { Image(systemName: "xmark.circle.fill").foregroundStyle(Color.tx3) }
                    .buttonStyle(.plain)
                    .accessibilityLabel("Clear")
            }
        }
        .padding(.horizontal, 18)
        .frame(height: 52)
        .glassEffect(.regular.interactive(), in: .capsule)
        .padding(.horizontal, 14)
        .padding(.bottom, 6)
    }

    // MARK: Changes

    private func add(_ exerciseId: UUID) {
        template.items.append(TemplateItem(exerciseId: exerciseId, groupLabel: ""))
        query = ""
    }

    private func remove(_ i: Int) {
        template.items.remove(at: i)
        tidyGroups()
    }

    private func pair(_ a: Int, _ b: Int) {
        let group = [template.items[a].groupLabel, template.items[b].groupLabel].first { !$0.isEmpty }
            ?? String(UUID().uuidString.prefix(8))
        template.items[a].groupLabel = group
        template.items[b].groupLabel = group
    }

    /// A superset is neighbours sharing a group; anyone left without a neighbour leaves it.
    private func tidyGroups() {
        let items = template.items
        for i in items.indices where !items[i].groupLabel.isEmpty {
            let g = items[i].groupLabel
            let before = i > 0 && items[i - 1].groupLabel == g
            let after = i + 1 < items.count && items[i + 1].groupLabel == g
            if !before && !after { template.items[i].groupLabel = "" }
        }
    }

    private func save() {
        template.name = trimmedName
        do {
            try app.db.save(template)
            app.edited()
            dismiss()
        } catch {
            log.error("save template: \(error.localizedDescription)")
        }
    }

    /// Each exercise's name and when it was last done, with its top set.
    nonisolated private static func rowInfo(_ d: Database, _ db: AppDatabase, _ ids: [UUID]) throws -> [UUID: ExerciseMatch] {
        var out: [UUID: ExerciseMatch] = [:]
        for id in Set(ids) {
            guard let e = try Exercise.fetchOne(d, key: id) else { continue }
            let last = try db.lastTime(d, exerciseId: id, excluding: nil)
            let top = last?.1.max { (Maths.toKg($0.weight, $0.unit) ?? 0) < (Maths.toKg($1.weight, $1.unit) ?? 0) }
            let text = top.flatMap { $0.weight.isEmpty ? nil : Format.set($0, e.measurement) }
            out[id] = ExerciseMatch(exercise: e, lastDate: last?.0, lastText: text)
        }
        return out
    }
}

/// A name for the new-exercise sheet.
private struct NewName: Identifiable {
    let name: String
    var id: String { name }
}
