import GRDB
import MiloAPI
import SwiftProtobuf
import SwiftUI

/// Milo, opened from the Ask bar on any screen.
struct MiloView: View {
    @Environment(AppModel.self) private var app
    @Environment(\.dismiss) private var dismiss
    @State private var session: MiloSession?
    @State private var text = ""
    @State private var chip: String?
    @State private var path = NavigationPath()
    @State private var editingTemplate: Template?
    @FocusState private var focused: Bool


    var body: some View {
        NavigationStack(path: $path) {
            Group {
                if let session, !session.isEmpty {
                    Conversation(session: session, openRunning: { dismiss() }, openWorkout: { path.append(WorkoutRoute(id: $0)) },
                                 openExercise: { path.append(ExerciseRoute(id: $0)) },
                                 editTemplate: { id in editingTemplate = try? app.db.writer.read { try Template.fetchOne($0, key: id) } })
                } else {
                    MiloHome { suggestion in Task { await send(suggestion) } } open: { id in
                        Task { await session?.open(id) }
                    }
                }
            }
            .safeAreaInset(edge: .bottom) {
                VStack(alignment: .leading, spacing: 10) {
                    if let chip {
                        HStack(spacing: 6) {
                            Image(systemName: "doc").font(.system(size: 12))
                            Text(chip).font(.sofia(13.5, .medium))
                            Button { self.chip = nil } label: { Image(systemName: "xmark").font(.system(size: 11, weight: .bold)) }
                                .accessibilityLabel("Don't send this context")
                        }
                        .foregroundStyle(Color.tx2)
                        .padding(.horizontal, 12).frame(height: 32)
                        .background(Color.s1, in: .capsule)
                        .overlay(Capsule().strokeBorder(Color.line2))
                    }
                    Composer(text: $text, placeholder: session?.isEmpty == false ? "Message Milo" : "Message Milo", busy: session?.busy == true, focused: $focused) {
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
                    HStack(spacing: 7) { BarMLogo(size: 16); Text("Milo").font(.sofia(17, .bold)) }
                }
                ToolbarItem(placement: .topBarTrailing) {
                    Button { session?.reset(); chip = app.miloContext.chip } label: { Image(systemName: "square.and.pencil").foregroundStyle(Color.tx) }
                        .accessibilityLabel("New conversation")
                }
            }
            .navigationBarTitleDisplayMode(.inline)
            .miloDestinations()
        }
        .presentationDragIndicator(.visible)
        .sheet(item: $editingTemplate) { TemplateEditor(template: $0) }
        .onAppear {
            if session == nil { session = MiloSession(app: app) }
            chip = app.miloContext.chip
        }
    }

    private func send(_ text: String) async {
        var context = app.miloContext
        if chip == nil { context = MiloContext(screen: context.screen, prompt: context.prompt) }
        await session?.send(text, context: context)
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
                        row(item, startsReply: startsReply).id(item.id)
                    }
                    if session.busy, case .user = session.items.last {
                        HStack(spacing: 7) { BarMLogo(size: 13); Text("Milo").font(.sofia(13, .semibold)) }
                            .foregroundStyle(Color.tx2).padding(.horizontal, 4)
                            .phaseAnimator([0.4, 1]) { v, p in v.opacity(p) } animation: { _ in .easeInOut(duration: 0.7) }
                    }
                }
                .padding(.horizontal, 16)
                .padding(.top, 12)
                .padding(.bottom, 16)
            }
            .scrollDismissesKeyboard(.interactively)
            .defaultScrollAnchor(.bottom)
            .onChange(of: session.items.count) { withAnimation { proxy.scrollTo(session.items.last?.id, anchor: .bottom) } }
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
            Image(systemName: "mic").font(.system(size: 17, weight: .medium)).foregroundStyle(Color.tx2)
            Button(action: send) {
                Image(systemName: "arrow.up").font(.system(size: 15, weight: .bold))
                    .foregroundStyle(empty || busy ? Color.tx3 : Color.bg)
                    .frame(width: 38, height: 38)
                    .background(empty || busy ? Color.s3 : Color.tx, in: .circle)
            }
            .buttonStyle(.plain)
            .disabled(empty || busy)
            .accessibilityLabel("Send")
        }
        .padding(.leading, 17).padding(.trailing, 7).padding(.vertical, 7)
        .glassEffect(.regular.interactive(), in: .rect(cornerRadius: 26))
    }
}

/// Milo before anything is said: what it can do, suggestions, earlier conversations.
private struct MiloHome: View {
    @Environment(AppModel.self) private var app
    @State private var earlier: [Milo_Conversation] = []
    let ask: (String) -> Void
    let open: (String) -> Void

    private let suggestions: [(String, String)] = [
        ("calendar", "What did I do last week?"),
        ("list.bullet", "Help me build a pull day template"),
        ("chart.line.uptrend.xyaxis", "Which lift has improved most?"),
        ("square.and.pencil", "Log yesterday from my notes"),
    ]

    var body: some View {
        ScrollView {
            VStack(alignment: .leading, spacing: 0) {
                Text("Ask, or tell me what to change.").font(.sofia(24, .bold)).tracking(-0.3)
                Text("I can read everything you've logged and edit any of it. You see each change before it's saved, and you can undo it.")
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
                                Image(systemName: "chevron.right").font(.system(size: 13, weight: .semibold)).foregroundStyle(Color.tx3)
                            }
                            .padding(.horizontal, 14).frame(minHeight: 56).contentShape(.rect)
                        }
                        .buttonStyle(.plain)
                    }
                }
                .background(Color.s1, in: .rect(cornerRadius: Radius.card))
                .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
                .padding(.top, 20)

                if !earlier.isEmpty {
                    SectionHeader("Earlier")
                    VStack(spacing: 0) {
                        ForEach(Array(earlier.enumerated()), id: \.element.id) { i, c in
                            if i > 0 { Divider().overlay(Color.line).padding(.leading, 14) }
                            Button { open(c.id) } label: {
                                HStack {
                                    VStack(alignment: .leading, spacing: 2) {
                                        Text(c.title).font(.sofia(16, .semibold)).lineLimit(1)
                                        if !c.summary.isEmpty { Text(c.summary).font(.sofia(14, .regular)).foregroundStyle(Color.tx2).lineLimit(1) }
                                    }
                                    Spacer(minLength: 6)
                                    Text(Format.shortDay(c.updatedAt.date)).font(.cap).foregroundStyle(Color.tx3)
                                    Image(systemName: "chevron.right").font(.system(size: 13, weight: .semibold)).foregroundStyle(Color.tx3)
                                }
                                .padding(.horizontal, 14).frame(minHeight: 58).contentShape(.rect)
                            }
                            .buttonStyle(.plain)
                        }
                    }
                    .background(Color.s1, in: .rect(cornerRadius: Radius.card))
                    .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
                }
            }
            .padding(.horizontal, 16)
            .padding(.top, 12)
        }
        .task {
            var req = Milo_ListConversationsRequest()
            req.limit = 10
            earlier = (try? await app.api.milo.listConversations(request: req, headers: APIClient.headers(app.session.token)).value().conversations) ?? []
        }
    }
}
