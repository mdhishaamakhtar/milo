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
                    Button { dismiss() } label: { Image(systemName: "xmark").foregroundStyle(Color.tx) }.accessibilityLabel("Close")
                }
                ToolbarItem(placement: .principal) {
                    HStack(spacing: 7) { BarMLogo(size: 16, working: session.busy); Text("Milo").font(.sofia(17, .bold)) }
                }
                ToolbarItemGroup(placement: .topBarTrailing) {
                    Button { path.append(ChatsRoute()) } label: { Image(systemName: "clock.arrow.circlepath").foregroundStyle(Color.tx) }
                        .accessibilityLabel("Chats")
                    Button { withAnimation(.settle) { session.reset() }; chip = app.miloContext.chip } label: { Image(systemName: "square.and.pencil").foregroundStyle(Color.tx) }
                        .accessibilityLabel("New chat")
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

    var body: some View {
        ScrollViewReader { proxy in
            ScrollView {
                LazyVStack(alignment: .leading, spacing: 14) {
                    ForEach(Array(session.items.enumerated()), id: \.element.id) { i, item in
                        let startsReply = i == 0 || { if case .user = session.items[i - 1] { return true } else { return false } }()
                        row(item, startsReply: startsReply).id(item.id).transition(.arrive)
                    }
                    if session.isWorking {
                        // Milo is reading or deciding: its mark moves until the next thing arrives.
                        HStack(spacing: 7) {
                            BarMLogo(size: 13, working: true)
                            if case .user = session.items.last { Text("Milo").font(.sofia(13, .semibold)).foregroundStyle(Color.tx2) }
                        }
                        .padding(.horizontal, 4)
                        .frame(height: 20)
                        .transition(.opacity)
                        .accessibilityElement()
                        .accessibilityLabel("Milo is working")
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

    @ViewBuilder private func row(_ item: MiloSession.Item, startsReply: Bool) -> some View {
        switch item {
        case .user(_, let text):
            Text(text)
                .font(.body16)
                .padding(.horizontal, 15).padding(.vertical, 11)
                .background(Color.s2, in: UnevenRoundedRectangle(topLeadingRadius: 22, bottomLeadingRadius: 22, bottomTrailingRadius: 7, topTrailingRadius: 22))
                .overlay(UnevenRoundedRectangle(topLeadingRadius: 22, bottomLeadingRadius: 22, bottomTrailingRadius: 7, topTrailingRadius: 22).strokeBorder(Color.line))
                .frame(maxWidth: 292, alignment: .trailing)
                .frame(maxWidth: .infinity, alignment: .trailing)
        case .reply(_, let text):
            VStack(alignment: .leading, spacing: 8) {
                if startsReply { MiloLabel() }
                Text(LocalizedStringKey(text)).font(.body16).lineSpacing(3).padding(.horizontal, 4)
            }
        case .reading(_, let summary):
            VStack(alignment: .leading, spacing: 8) {
                if startsReply { MiloLabel() }
                Label(summary, systemImage: summary.hasPrefix("Read") ? "doc.text" : "chart.line.uptrend.xyaxis")
                    .font(.sofia(13.5, .medium)).foregroundStyle(Color.tx3).padding(.horizontal, 4)
            }
        case .card(let card):
            CardView(card: card, session: session, onAdded: onAdded, openRunning: openRunning, openWorkout: openWorkout, openExercise: openExercise, editTemplate: editTemplate)
        case .problem(_, let text):
            Label(text, systemImage: "exclamationmark.circle")
                .font(.sub).foregroundStyle(Color.tx2).padding(.horizontal, 4)
        }
    }
}

struct MiloLabel: View {
    var body: some View {
        HStack(spacing: 7) { BarMLogo(size: 13); Text("Milo").font(.sofia(13, .semibold)).foregroundStyle(Color.tx2) }
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
