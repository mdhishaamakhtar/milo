import Connect
import MiloAPI
import SwiftProtobuf
import SwiftUI

/// Pushed from Milo's toolbar.
struct ChatsRoute: Hashable {}

/// Every conversation with Milo, newest first, grouped by when. Tap one to pick
/// it back up; swipe to delete it.
struct ChatsView: View {
    @Environment(AppModel.self) private var app
    @State private var state = LoadState.loading
    @State private var search = ""
    /// Opens a conversation by id.
    let open: (String) -> Void

    private enum LoadState {
        case loading, failed
        case loaded([Milo_Conversation])
    }

    var body: some View {
        Group {
            switch state {
            case .loading:
                ProgressView().frame(maxWidth: .infinity, maxHeight: .infinity)
            case .failed:
                ContentUnavailableView {
                    Label("Couldn't load your chats", systemImage: "wifi.slash")
                } description: {
                    Text("Milo needs a connection for these.")
                } actions: {
                    Button("Try again") { Task { await load() } }.buttonStyle(.chip)
                }
            case .loaded(let chats) where chats.isEmpty:
                ContentUnavailableView("No chats yet", systemImage: "bubble.left.and.text.bubble.right", description: Text("Conversations with Milo are kept here."))
            case .loaded(let chats):
                list(chats)
            }
        }
        .miloScreen()
        .navigationTitle("Chats")
        .navigationBarTitleDisplayMode(.inline)
        .task { await load() }
    }

    private func list(_ chats: [Milo_Conversation]) -> some View {
        let shown = search.isEmpty ? chats : chats.filter { $0.title.localizedCaseInsensitiveContains(search) || $0.summary.localizedCaseInsensitiveContains(search) }
        return List {
            ForEach(ChatGroup.groups(shown)) { group in
                Section {
                    ForEach(group.chats, id: \.id) { chat in
                        Button { open(chat.id) } label: { ChatRow(chat: chat, isOpen: chat.id == app.milo.conversationID, showsTime: group.showsTime) }
                            .buttonStyle(.row)
                            .listRowBackground(Color.bg)
                            .listRowSeparatorTint(Color.line)
                            .listRowInsets(EdgeInsets(top: 12, leading: 20, bottom: 12, trailing: 20))
                            .alignmentGuide(.listRowSeparatorLeading) { _ in 0 }
                            .swipeActions {
                                Button("Delete", systemImage: "trash", role: .destructive) { delete(chat) }.tint(Color.red)
                            }
                            .contextMenu {
                                Button("Delete chat", systemImage: "trash", role: .destructive) { delete(chat) }
                            }
                    }
                } header: {
                    Text(group.title).font(.cap).foregroundStyle(Color.tx3).textCase(nil)
                }
            }
        }
        .listStyle(.plain)
        .scrollContentBackground(.hidden)
        .searchable(text: $search, prompt: "Search chats")
        .overlay {
            if shown.isEmpty { ContentUnavailableView.search(text: search) }
        }
        .refreshable { await load() }
        .animation(.settle, value: shown.map(\.id))
    }

    private func load() async {
        var request = Milo_ListConversationsRequest()
        request.limit = 100
        do {
            state = .loaded(try await app.api.milo.listConversations(request: request, headers: APIClient.headers(app.session.token)).value().conversations)
        } catch {
            if case .loaded = state { return } // a failed refresh keeps what's showing
            state = .failed
        }
    }

    /// Removes the chat here straight away, then on the server.
    private func delete(_ chat: Milo_Conversation) {
        guard case .loaded(let chats) = state else { return }
        state = .loaded(chats.filter { $0.id != chat.id })
        if chat.id == app.milo.conversationID { app.milo.reset() }
        Task {
            var request = Milo_DeleteConversationRequest()
            request.id = chat.id
            let response = await app.api.milo.deleteConversation(request: request, headers: APIClient.headers(app.session.token))
            if response.error != nil { await load() } // it's still there; show it again
        }
    }
}

/// One chat: how it began, what came of it, and when.
private struct ChatRow: View {
    let chat: Milo_Conversation
    let isOpen: Bool
    let showsTime: Bool

    var body: some View {
        HStack(alignment: .firstTextBaseline, spacing: 12) {
            VStack(alignment: .leading, spacing: 4) {
                Text(chat.title).font(.sofia(16.5, .semibold)).lineLimit(2).multilineTextAlignment(.leading)
                if !chat.summary.isEmpty {
                    Text(chat.summary).font(.sub).foregroundStyle(Color.tx2).lineLimit(2).multilineTextAlignment(.leading)
                }
            }
            Spacer(minLength: 0)
            if isOpen {
                Badge(text: "Open", color: .tx2)
            } else {
                Text(showsTime ? chat.updatedAt.date.formatted(date: .omitted, time: .shortened) : Format.shortDay(chat.updatedAt.date))
                    .font(.sofia(13.5, .medium)).foregroundStyle(Color.tx3).monospacedDigit()
            }
        }
        .frame(maxWidth: .infinity, alignment: .leading)
        .contentShape(.rect)
    }
}

/// Chats under one heading: Today, Yesterday, Earlier this week, then by month.
nonisolated struct ChatGroup: Identifiable {
    let title: String
    /// Today's and yesterday's rows show a time; older ones show the day.
    let showsTime: Bool
    var chats: [Milo_Conversation]
    var id: String { title }

    static func groups(_ chats: [Milo_Conversation], now: Date = .now, calendar: Calendar = .current) -> [ChatGroup] {
        var groups: [ChatGroup] = []
        for chat in chats.sorted(by: { $0.updatedAt.date > $1.updatedAt.date }) {
            let (title, showsTime) = heading(chat.updatedAt.date, now: now, calendar: calendar)
            if groups.last?.title == title {
                groups[groups.count - 1].chats.append(chat)
            } else {
                groups.append(ChatGroup(title: title, showsTime: showsTime, chats: [chat]))
            }
        }
        return groups
    }

    private static func heading(_ date: Date, now: Date, calendar: Calendar) -> (String, Bool) {
        let days = calendar.dateComponents([.day], from: calendar.startOfDay(for: date), to: calendar.startOfDay(for: now)).day ?? 0
        switch days {
        case ...0: return ("Today", true)
        case 1: return ("Yesterday", true)
        case 2...6: return ("Earlier this week", false)
        default: return (date.formatted(.dateTime.month(.wide).year()), false)
        }
    }
}
