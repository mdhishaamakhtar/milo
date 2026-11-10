import GRDB
import MiloAPI
import SwiftProtobuf
import SwiftUI

/// Milo, opened from the Ask bar on any screen. The conversation stays as it was
/// when the sheet is reopened; earlier ones are under Chats.
struct MiloView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var text = ""
    @State private var chip: String?
    @State private var path = NavigationPath()
    @State private var editingTemplate: Template?
    @FocusState private var focused: Bool

    private var session: MiloSession { app.milo }

    var body: some View {
        NavigationStack(path: $path) {
            Group {
                if session.isEmpty {
                    MiloHome { suggestion in Task { await send(suggestion) } }
                } else {
                    Conversation(session: session, openRunning: { dismiss() }, openWorkout: { path.append(WorkoutRoute(id: $0)) },
                                 openExercise: { path.append(ExerciseRoute(id: $0)) },
                                 editTemplate: { id in editingTemplate = try? app.db.writer.read { try Template.fetchOne($0, key: id) } })
                }
            }
            .safeAreaInset(edge: .bottom) {
                VStack(alignment: .leading, spacing: 10) {
                    if let chip {
                        HStack(spacing: 6) {
                            Image(systemName: "doc").font(.system(size: 12))
                            Text(chip).font(.sofia(13.5, .medium))
                            Button { withAnimation(.quick) { self.chip = nil } } label: { Image(systemName: "xmark").font(.system(size: 11, weight: .bold)) }
                                .accessibilityLabel("Don't send this context")
                        }
                        .foregroundStyle(Color.tx2)
                        .padding(.horizontal, 12).frame(height: 32)
                        .background(Color.s1, in: .capsule)
                        .overlay(Capsule().strokeBorder(Color.line2))
                        .transition(.opacity)
                    }
                    Composer(text: $text, placeholder: "Message Milo", busy: session.busy, focused: $focused) {
                        let t = text
                        text = ""
                        Task { await send(t) }
                    }
                }
                .padding(.horizontal, 14)
                .padding(.bottom, 8)
            }
            .miloScreen()
            .toolbar {
                ToolbarItem(placement: .topBarLeading) {
                    Button(role: .close) { dismiss() }.tint(Color.tx)
                }
                ToolbarItem(placement: .principal) {
                    HStack(spacing: 7) { BarMLogo(size: 16, working: session.busy); Text("Milo").font(.sofia(17, .bold)) }
                }
                ToolbarItemGroup(placement: .topBarTrailing) {
                    Button { path.append(ChatsRoute()) } label: { Label("Chats", systemImage: "clock.arrow.circlepath") }
                        .tint(Color.tx)
                    Button { withAnimation(.settle) { session.reset() }; chip = app.miloContext.chip } label: { Label("New chat", systemImage: "square.and.pencil") }
                        .tint(Color.tx)
                        .disabled(session.isEmpty)
                }
            }
            .navigationBarTitleDisplayMode(.inline)
            .miloDestinations()
            .navigationDestination(for: ChatsRoute.self) { _ in
                ChatsView { id in
                    path = NavigationPath()
                    Task { await session.open(id) }
                }
            }
        }
        .presentationDragIndicator(.visible)
        .sheet(item: $editingTemplate) { TemplateEditor(template: $0) }
        .onAppear {
            session.resetIfIdle()
            chip = app.miloContext.chip
        }
    }

    private func send(_ text: String) async {
        var context = app.miloContext
        if chip == nil { context = MiloContext(screen: context.screen, prompt: context.prompt) }
        await session.send(text, context: context)
    }
}

/// What Milo says and shows, in order.
struct Conversation: View {
    let session: MiloSession
    var onAdded: (() -> Void)?
    var openRunning: (() -> Void)?
    var openWorkout: ((UUID) -> Void)?
    var openExercise: ((UUID) -> Void)?
    var editTemplate: ((UUID) -> Void)?

    /// One line of the conversation. What Milo read in a row is one line, not one per read.
    private struct Row: Identifiable {
        let id: String
        let item: MiloSession.Item
        var readings: [String] = []
        let startsReply: Bool
    }

    private var rows: [Row] {
        var rows: [Row] = []
        for item in session.items {
            let startsReply = rows.last.map { if case .user = $0.item { true } else { false } } ?? true
            if case .reading(_, let summary) = item {
                if let last = rows.indices.last, case .reading = rows[last].item {
                    rows[last].readings.append(summary)
                } else {
                    rows.append(Row(id: item.id, item: item, readings: [summary], startsReply: startsReply))
                }
            } else {
                rows.append(Row(id: item.id, item: item, startsReply: startsReply))
            }
        }
        return rows
    }

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 16) {
                    ForEach(rows) { row in
                        self.row(row).id(row.id).transition(Self.arrival(row.item))
                    }
                    if session.isWorking {
                        // Sits where the reply's "Milo" label will be, so when the answer starts
                        // the words change in place and the text flows in below.
                        ThinkingRow(reading: session.hasReadSinceAsking).transition(.opacity)
                    }
                }
                .padding(.horizontal, 16)
                .padding(.top, 12)
                .padding(.bottom, 16)
                .animation(.settle, value: session.items.count)
                .animation(.settle, value: session.isWorking)
            }
            .scrollDismissesKeyboard(.interactively)
            .defaultScrollAnchor(.bottom)
            // Stay at the bottom while a reply streams in and grows.
            .defaultScrollAnchor(.bottom, for: .sizeChanges)
            .onChange(of: session.items.count) { withAnimation(.settle) { proxy.scrollTo(session.items.last?.id, anchor: .bottom) } }
        }
    }

    /// What you send rises from the message field into the conversation; Milo's side fades up.
    private static func arrival(_ item: MiloSession.Item) -> AnyTransition {
        guard case .user = item else { return .arrive }
        return .asymmetric(
            insertion: .opacity.combined(with: .offset(y: 28)).combined(with: .scale(scale: 0.96, anchor: .bottomTrailing)),
            removal: .opacity)
    }

    @ViewBuilder private func row(_ row: Row) -> some View {
        switch row.item {
        case .user(_, let text):
            UserBubble(text: text)
        case .reply(_, let text):
            VStack(alignment: .leading, spacing: 10) {
                if row.startsReply { MiloLabel() }
                ReplyText(text: text)
            }
        case .reading:
            VStack(alignment: .leading, spacing: 10) {
                if row.startsReply { MiloLabel() }
                ReadingsRow(summaries: row.readings)
            }
        case .card(let card):
            CardView(card: card, session: session, onAdded: onAdded, openRunning: openRunning, openWorkout: openWorkout, openExercise: openExercise, editTemplate: editTemplate)
        case .problem(_, let text):
            Label(text, systemImage: "exclamationmark.circle")
                .font(.sub).foregroundStyle(Color.tx2).padding(.horizontal, 4)
        }
    }
}

/// What Milo read before it answered: the one thing it read, or how many, opening to the list.
private struct ReadingsRow: View {
    let summaries: [String]
    @State private var open = false

    var body: some View {
        if summaries.count == 1 {
            Label(summaries[0], systemImage: "doc.text")
                .font(.sofia(13.5, .medium)).foregroundStyle(Color.tx3)
                .padding(.horizontal, 4)
        } else {
            VStack(alignment: .leading, spacing: 8) {
                Button { withAnimation(.settle) { open.toggle() } } label: {
                    HStack(spacing: 6) {
                        Label("Read \(summaries.count) things from your log", systemImage: "doc.text.magnifyingglass")
                        Image(systemName: "chevron.right")
                            .font(.system(size: 10, weight: .bold))
                            .rotationEffect(.degrees(open ? 90 : 0))
                    }
                    .font(.sofia(13.5, .medium)).foregroundStyle(Color.tx3)
                    .padding(.horizontal, 4)
                    .frame(minHeight: 28)
                    .contentShape(.rect)
                }
                .buttonStyle(.row)
                .accessibilityValue(open ? "Shown" : "Hidden")
                if open {
                    VStack(alignment: .leading, spacing: 7) {
                        ForEach(Array(summaries.enumerated()), id: \.offset) { _, summary in
                            Text(summary)
                        }
                    }
                    .font(.sofia(13.5)).foregroundStyle(Color.tx3)
                    .padding(.leading, 14)
                    .overlay(alignment: .leading) { Capsule().fill(Color.line2).frame(width: 2).padding(.leading, 4) }
                    .transition(.opacity.combined(with: .offset(y: -4)))
                }
            }
        }
    }
}

/// Milo at work. Its bars move and a band of light runs across what it is doing, so a wait
/// reads as thinking, not a frozen screen. After a few seconds the words own up to the wait.
private struct ThinkingRow: View {
    /// It has started reading from the log.
    let reading: Bool
    @State private var slow = false

    private var words: String {
        switch (reading, slow) {
        case (false, false): "Thinking"
        case (false, true): "Still thinking"
        case (true, false): "Reading your log"
        case (true, true): "Putting it together"
        }
    }

    var body: some View {
        HStack(spacing: 7) {
            BarMLogo(size: 13, working: true)
            ShimmerText(text: words).id(words).transition(.opacity)
        }
        .padding(.horizontal, 4)
        .frame(height: 20)
        .animation(.settle, value: words)
        .task(id: reading) {
            slow = false
            do {
                try await Task.sleep(for: .seconds(6))
                withAnimation(.settle) { slow = true }
            } catch {}
        }
        .accessibilityElement()
        .accessibilityLabel("Milo: \(words)")
    }
}

/// Text with a soft band of light passing over it, left to right. Still under Reduce Motion.
private struct ShimmerText: View {
    let text: String
    @Environment(\.accessibilityReduceMotion) private var reduceMotion

    var body: some View {
        TimelineView(.animation(paused: reduceMotion)) { context in
            Text(text)
                .font(.sofia(13, .semibold))  // the reply's "Milo" label, which it turns into
                .foregroundStyle(LinearGradient(stops: Self.stops(reduceMotion ? -1 : Self.phase(context.date)), startPoint: .leading, endPoint: .trailing))
        }
    }

    /// Where the band is, from just off the left edge to just off the right, every 1.8 s.
    private static func phase(_ date: Date) -> Double {
        date.timeIntervalSinceReferenceDate.truncatingRemainder(dividingBy: 1.8) / 1.8 * 1.6 - 0.3
    }

    private static func stops(_ p: Double) -> [Gradient.Stop] {
        func at(_ x: Double) -> Double { min(1, max(0, x)) }
        return [
            .init(color: .tx2, location: 0),
            .init(color: .tx2, location: at(p - 0.22)),
            .init(color: .tx, location: at(p)),
            .init(color: .tx2, location: at(p + 0.22)),
            .init(color: .tx2, location: 1),
        ]
    }
}

/// What you said to Milo, on the right.
struct UserBubble: View {
    let text: String

    var body: some View {
        let shape = UnevenRoundedRectangle(topLeadingRadius: 22, bottomLeadingRadius: 22, bottomTrailingRadius: 7, topTrailingRadius: 22)
        Text(text)
            .font(.body16)
            .padding(.horizontal, 15).padding(.vertical, 11)
            .background(Color.s2, in: shape)
            .overlay(shape.strokeBorder(Color.line))
            .frame(maxWidth: 292, alignment: .trailing)
            .frame(maxWidth: .infinity, alignment: .trailing)
    }
}

struct MiloLabel: View {
    var working = false

    var body: some View {
        HStack(spacing: 7) { BarMLogo(size: 13, working: working); Text("Milo").font(.sofia(13, .semibold)).foregroundStyle(Color.tx2) }
            .padding(.horizontal, 4)
    }
}

/// The message field: glass, a mic, and send.
struct Composer: View {
    @Binding var text: String
    var placeholder: String
    var busy: Bool
    var focused: FocusState<Bool>.Binding
    let send: () -> Void

    private var empty: Bool { text.trimmingCharacters(in: .whitespacesAndNewlines).isEmpty }

    var body: some View {
        HStack(spacing: 10) {
            TextField(placeholder, text: $text, axis: .vertical)
                .font(.sofia(16.5, .regular))
                .lineLimit(1...5)
                .focused(focused)
                .onSubmit { if !empty && !busy { send() } }
            MicButton(text: $text)
            Button(action: send) {
                Image(systemName: "arrow.up").font(.system(size: 15, weight: .bold))
                    .foregroundStyle(empty || busy ? Color.tx3 : Color.bg)
                    .frame(width: 38, height: 38)
                    .background(empty || busy ? Color.s3 : Color.tx, in: .circle)
                    .animation(.quick, value: empty || busy)
            }
            .buttonStyle(.row)
            .disabled(empty || busy)
            .accessibilityLabel("Send")
        }
        .padding(.leading, 17).padding(.trailing, 7).padding(.vertical, 7)
        .glassEffect(.regular.interactive(), in: .rect(cornerRadius: 26))
    }
}

/// Milo before anything is said: what it can do, and a few things to try.
private struct MiloHome: View {
    let ask: (String) -> Void

    private let suggestions: [(String, String)] = [
        ("play", "Start my next workout"),
        ("calendar", "What did I do last week?"),
        ("chart.line.uptrend.xyaxis", "Which lift has improved most?"),
        ("list.bullet", "Help me build a pull day template"),
        ("square.and.pencil", "Log yesterday from my notes"),
    ]

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                Text("Ask, or tell me what to do.").font(.sofia(24, .bold)).tracking(-0.3)
                Text("I can do anything in your log that you can: start and finish workouts, log and fix sets, build templates, answer questions. Anything I change, you can undo.")
                    .font(.sofia(15.5, .regular)).foregroundStyle(Color.tx2).padding(.top, 8)
                VStack(spacing: 0) {
                    ForEach(Array(suggestions.enumerated()), id: \.offset) { i, s in
                        if i > 0 { Divider().overlay(Color.line).padding(.leading, 60) }
                        Button { ask(s.1) } label: {
                            HStack(spacing: 12) {
                                Image(systemName: s.0).font(.system(size: 14)).foregroundStyle(Color.tx2)
                                    .frame(width: 32, height: 32).background(Color.s2, in: .rect(cornerRadius: 10))
                                Text(s.1).font(.sofia(16, .medium)).multilineTextAlignment(.leading)
                                Spacer(minLength: 4)
                                Image(systemName: "arrow.up.right").font(.system(size: 12, weight: .semibold)).foregroundStyle(Color.tx3)
                            }
                            .padding(.horizontal, 14).frame(minHeight: 56).contentShape(.rect)
                        }
                        .buttonStyle(.row)
                    }
                }
                .background(Color.s1, in: .rect(cornerRadius: Radius.card))
                .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
                .padding(.top, 20)
            }
            .padding(.horizontal, 16)
            .padding(.top, 12)
        }
        .scrollDismissesKeyboard(.interactively)
    }
}
