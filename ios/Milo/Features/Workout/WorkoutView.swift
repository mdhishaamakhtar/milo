import SwiftUI

/// The running workout, full screen. The chevron tucks it away; Today shows it live.
/// Given a finished workout's id, the same screen edits that workout.
struct WorkoutView: View {
    @Environment(AppModel.self) private var app
    var workoutID: UUID?
    @State private var model: WorkoutModel?
    @State private var milo: MiloSession?

    var body: some View {
        Group {
            if let model, let milo {
                WorkoutScreen(milo: milo).environment(model)
            } else {
                Color.bg.ignoresSafeArea()
            }
        }
        .onAppear {
            guard model == nil else { return }
            let m = WorkoutModel(app: app, workoutID: workoutID)
            m.watch()
            model = m
            milo = MiloSession(app: app)
        }
    }
}

private struct WorkoutScreen: View {
    @Environment(AppModel.self) private var app
    @Environment(WorkoutModel.self) private var model
    @Environment(\.dismiss) private var dismiss
    let milo: MiloSession
    @State private var talking = false
    @State private var finishing = false
    @State private var droppingUnticked = false
    /// The screen underneath's Milo context, handed back on the way out.
    @State private var previousContext: MiloContext?
    @FocusState private var fieldFocused: Bool

    var body: some View {
        NavigationStack {
            // One stable container: an inset on a ScrollView belongs to that scroll view, and
            // switching content would rebuild the add field and drop its focus.
            ZStack { content }
                .safeAreaInset(edge: .bottom) {
                    bottom
                        .animation(.settle, value: model.editing != nil)
                        .animation(.settle, value: model.restEndsAt != nil)
                }
                .miloScreen()
                .toolbar { toolbar }
                .navigationBarTitleDisplayMode(.inline)
                .navigationDestination(isPresented: $finishing) { FinishView() }
        }
        .undoBar(above: 84)
        .onChange(of: model.detail.value == nil) { _, gone in if gone { dismiss() } }
        // An exercise went in: put the keyboard away so its sets show.
        .onChange(of: model.entries.count) { old, new in if new > old { fieldFocused = false } }
        .onAppear {
            previousContext = app.miloContext
            if let w = model.detail.value?.workout {
                app.miloContext = MiloContext(screen: "workout", prompt: "Next exercise, or tell Milo", workoutID: w.id)
            }
        }
        .onDisappear {
            if let previousContext { app.miloContext = previousContext }
        }
    }

    @ViewBuilder private var content: some View {
        if talking {
            Conversation(session: milo, say: ask, onAdded: added)
        } else if !model.query.trimmingCharacters(in: .whitespaces).isEmpty {
            ScrollView { MatchList(ask: ask).padding(.horizontal, 16).padding(.top, 8) }
                .scrollDismissesKeyboard(.interactively)
        } else if model.entries.isEmpty {
            EmptyStart(ask: ask)
        } else {
            ScrollViewReader { proxy in
                ScrollView {
                    VStack(alignment: .leading, spacing: 14) {
                        if !model.finished.isEmpty { FinishedCard(entries: model.finished) }
                        if let current = model.current {
                            ExerciseCard(entry: current).id(current.id).transition(.arrive)
                        }
                        if !model.upNext.isEmpty { UpNext(entries: model.upNext) }
                    }
                    .padding(.horizontal, 16)
                    .padding(.top, 8)
                    .padding(.bottom, 16)
                    // Exercises change place as they are finished, opened or added.
                    .animation(.settle, value: model.current?.id)
                    .animation(.settle, value: model.entries.map(\.id))
                }
                .scrollDismissesKeyboard(.interactively)
                .onChange(of: model.current?.id) { _, id in withAnimation(.settle) { proxy.scrollTo(id, anchor: .top) } }
            }
        }
    }

    @ViewBuilder private var bottom: some View {
        if model.editing != nil {
            NumberPad().transition(.move(edge: .bottom).combined(with: .opacity))
        } else {
            // The rest timer and the add field are glass side by side: one container lets them
            // render together and the timer grow out of the field.
            GlassEffectContainer(spacing: 8) {
            VStack(spacing: 8) {
                if let end = model.restEndsAt, !talking {
                    RestBar(endsAt: end).transition(.move(edge: .bottom).combined(with: .opacity))
                }
                AddField(
                    placeholder: talking ? "Correct it, e.g. \"it was a cable\"" : model.entries.isEmpty ? "Exercise name, or describe it" : model.isPast ? "Add an exercise, or tell Milo" : "Next exercise, or tell Milo",
                    focused: $fieldFocused,
                    ask: ask)
            }
            }
            .padding(.horizontal, 14)
            .padding(.bottom, 6)
        }
    }

    @ToolbarContentBuilder private var toolbar: some ToolbarContent {
        ToolbarItem(placement: .topBarLeading) {
            Group {
                if talking {
                    Button { withAnimation(.settle) { talking = false } } label: { Label("Back to the exercises", systemImage: "chevron.left") }
                } else if !model.isPast {
                    Button { dismiss() } label: { Label("Minimise workout", systemImage: "chevron.down") }
                }
            }
            .tint(Color.tx)
        }
        ToolbarItem(placement: .principal) {
            if let w = model.detail.value?.workout {
                VStack(spacing: 1) {
                    Text(w.name).font(.sofia(16.5, .semibold)).lineLimit(1)
                    if model.isPast {
                        Text("Editing, \(Format.shortDay(w.startedAt))").font(.sofia(14, .medium)).foregroundStyle(Color.tx2)
                    } else {
                        HStack(spacing: 5) {
                            Circle().fill(Color.or).frame(width: 6, height: 6)
                            Text(timerInterval: w.startedAt...Date.distantFuture, countsDown: false)
                                .font(.sofia(14, .medium)).monospacedDigit().foregroundStyle(Color.tx2)
                        }
                    }
                }
            }
        }
        ToolbarItem(placement: .topBarTrailing) {
            Group {
                if model.isPast {
                    Button("Done") {
                        if model.untickedCount > 0 { droppingUnticked = true } else { model.saveEdits(); dismiss() }
                    }
                    .font(.sofia(16, .bold))
                    .buttonStyle(.glassProminent)
                    .tint(Color.tx)
                    .foregroundStyle(Color.bg)
                    .confirmationDialog("\(model.untickedCount) \(model.untickedCount == 1 ? "set isn't" : "sets aren't") ticked", isPresented: $droppingUnticked, titleVisibility: .visible) {
                        Button("Save without them") { model.saveEdits(); dismiss() }
                        Button("Keep editing", role: .cancel) {}
                    } message: {
                        Text("Only ticked sets are kept.")
                    }
                } else {
                    // Always straight to the Finish screen: no pop-up to dismiss and re-tap.
                    // With nothing ticked, that screen says so and offers Discard.
                    Button("Finish") {
                        model.closePad()
                        finishing = true
                    }
                    .font(.sofia(16, .bold))
                    .buttonStyle(.glassProminent)
                    .tint(Color.tx)
                    .foregroundStyle(Color.bg)
                }
            }
        }
    }

    private func ask(_ text: String) {
        fieldFocused = false
        withAnimation(.settle) { talking = true }
        let context = MiloContext(screen: "workout", prompt: "", workoutID: model.detail.value?.workout.id)
        Task { await milo.send(text, context: context) }
    }

    /// A new exercise from Milo went into the workout: show it open.
    private func added() {
        withAnimation(.settle) { talking = false }
        if let id = milo.addedWorkoutExercise { model.openID = id }
    }
}

/// A blank workout: what usually comes first, a template, or a pasted plan.
private struct EmptyStart: View {
    @Environment(AppModel.self) private var app
    @Environment(WorkoutModel.self) private var model
    @State private var firsts = Live<[ExerciseMatch]>([])
    @State private var templates = Live<[TemplateSummary]>([])
    let ask: (String) -> Void

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                Text("What's first?").font(.sofia(30, .bold)).tracking(-0.4)
                Text("Type the exercise the way your trainer says it. If it isn't in your list, Milo works out what it is.")
                    .font(.sofia(15.5, .regular)).foregroundStyle(Color.tx2).padding(.top, 8)
                if !firsts.value.isEmpty {
                    Text("You usually start with").font(.h3).padding(.top, 26).padding(.bottom, 12)
                    VStack(spacing: 0) {
                        ForEach(Array(firsts.value.enumerated()), id: \.element.id) { i, m in
                            if i > 0 { Divider().overlay(Color.line).padding(.leading, 16) }
                            Button { model.add(m.exercise) } label: {
                                HStack {
                                    VStack(alignment: .leading, spacing: Space.rowText) {
                                        Text(m.exercise.name).font(.sofia(16, .semibold))
                                        if let d = m.lastDate {
                                            Text("Last \(Format.shortDay(d))" + (m.lastText.map { ", \($0)" } ?? "")).font(.sofia(14, .regular)).foregroundStyle(Color.tx2)
                                        }
                                    }
                                    Spacer()
                                    Image(systemName: "plus").font(.system(size: 15, weight: .semibold))
                                        .frame(width: 34, height: 34).background(Color.s2, in: .circle)
                                }
                                .padding(.horizontal, 16).frame(minHeight: 58).contentShape(.rect)
                            }
                            .buttonStyle(.row)
                        }
                    }
                    .background(Color.s1, in: .rect(cornerRadius: Radius.card))
                    .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
                }
                HStack(spacing: 8) {
                    if !templates.value.isEmpty {
                        Menu {
                            ForEach(templates.value) { t in
                                Button(t.template.name) { useTemplate(t.template) }
                            }
                        } label: { Label("Use a template", systemImage: "line.3.horizontal") }
                    }
                    Button { if let plan = UIPasteboard.general.string, !plan.isEmpty { ask("Here's today's plan; set up the exercises:\n" + plan) } } label: {
                        Label("Paste a plan", systemImage: "doc")
                    }
                }
                .buttonStyle(.chip)
                .labelStyle(ChipLabelStyle())
                .padding(.top, 14)
            }
            .padding(.horizontal, 16)
            .padding(.top, 20)
        }
        .onAppear {
            let db = app.db
            firsts.watch(db) { try db.usualFirsts($0) }
            templates.watch(db) { try db.templates($0) }
        }
    }

    private func useTemplate(_ t: Template) {
        guard let w = model.detail.value?.workout else { return }
        for item in t.items { _ = try? app.db.addExercise(item.exerciseId, to: w.id, groupLabel: item.groupLabel) }
        var renamed = w
        renamed.name = t.name
        renamed.templateId = t.id
        try? app.db.save(renamed)
        app.edited()
    }
}

/// Exercises already done, one line each.
private struct FinishedCard: View {
    @Environment(WorkoutModel.self) private var model
    let entries: [ExerciseEntry]

    var body: some View {
        VStack(spacing: 0) {
            ForEach(Array(entries.enumerated()), id: \.element.id) { i, e in
                if i > 0 { Divider().overlay(Color.line).padding(.leading, 52) }
                Button { withAnimation(.settle) { model.openID = e.id } } label: {
                    HStack(spacing: 12) {
                        ZStack {
                            Circle().fill(Color.or.opacity(0.16))
                            Image(systemName: "checkmark").font(.system(size: 11, weight: .bold)).foregroundStyle(Color.or)
                        }
                        .frame(width: 24, height: 24)
                        Text(e.exercise.name).font(.sofia(16, .semibold)).lineLimit(1)
                        Spacer(minLength: 8)
                        Text(Format.summary(e)).font(.sofia(14.5, .regular)).foregroundStyle(Color.tx2).lineLimit(1)
                    }
                    .padding(.horizontal, 16).frame(minHeight: 54).contentShape(.rect)
                }
                .buttonStyle(.row)
                .background(Color.s1)
                .swipeToDelete("Remove from workout", cornerRadius: 0) { model.remove(e) }
                .contextMenu { RemoveButton(entry: e) }
            }
        }
        .background(Color.s1)
        .clipShape(.rect(cornerRadius: Radius.card))
        .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
    }
}

/// "Remove from workout", asking first when sets were logged.
private struct RemoveButton: View {
    @Environment(WorkoutModel.self) private var model
    let entry: ExerciseEntry

    var body: some View {
        Button("Remove from workout", systemImage: "trash", role: .destructive) { model.remove(entry) }
    }
}

/// What's left, as a plain list.
private struct UpNext: View {
    @Environment(WorkoutModel.self) private var model
    let entries: [ExerciseEntry]

    var body: some View {
        VStack(alignment: .leading, spacing: 4) {
            Text("Up next").font(.cap).foregroundStyle(Color.tx3).padding(.horizontal, 4).padding(.top, 8)
            ForEach(entries) { e in
                Button { withAnimation(.settle) { model.openID = e.id } } label: {
                    HStack {
                        Text(e.exercise.name).font(.sofia(16, .medium))
                        Spacer()
                        Text("\(e.sets.count) \(e.sets.count == 1 ? "set" : "sets")").font(.sofia(14, .regular)).foregroundStyle(Color.tx3)
                    }
                    .padding(.horizontal, 4).frame(minHeight: 44).contentShape(.rect)
                }
                .buttonStyle(.row)
                .background(Color.bg)
                .swipeToDelete("Remove from workout") { model.remove(e) }
                .contextMenu { RemoveButton(entry: e) }
            }
        }
    }
}
