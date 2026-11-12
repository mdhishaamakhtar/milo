import Foundation
import MiloAPI
import SwiftProtobuf
import Testing
@testable import Milo

/// How Milo's reply text is broken into paragraphs and lists, including while it streams in.
struct ReplyTextTests {
    typealias Block = ReplyText.Block

    @Test func blankLinesSplitParagraphsAndLinesJoin() {
        #expect(ReplyText.blocks("Good session.\nBench went up.\n\nRest tomorrow.") == [
            .paragraph("Good session. Bench went up."),
            .paragraph("Rest tomorrow."),
        ])
    }

    @Test func dashStarAndDotLinesAreOneBulletList() {
        #expect(ReplyText.blocks("Today:\n- **Bench** 60 kg × 8\n* Squat 80 kg × 5\n• Rows") == [
            .paragraph("Today:"),
            .list(["**Bench** 60 kg × 8", "Squat 80 kg × 5", "Rows"], numbered: false),
        ])
    }

    @Test func numberedLinesAreANumberedList() {
        #expect(ReplyText.blocks("1. Warm up\n2. Bench\n10. Stretch") == [.list(["Warm up", "Bench", "Stretch"], numbered: true)])
    }

    @Test func switchingListKindStartsANewList() {
        #expect(ReplyText.blocks("- a\n1. b\n- c") == [
            .list(["a"], numbered: false),
            .list(["b"], numbered: true),
            .list(["c"], numbered: false),
        ])
    }

    @Test func aParagraphLineEndsAList() {
        #expect(ReplyText.blocks("- a\n- b\nThat's it.") == [.list(["a", "b"], numbered: false), .paragraph("That's it.")])
    }

    @Test func aHeadingBecomesABoldLine() {
        #expect(ReplyText.blocks("## This week\nThree sessions.") == [.paragraph("**This week**"), .paragraph("Three sessions.")])
        // A lone "#" isn't a heading: it shows as typed.
        #expect(ReplyText.blocks("#") == [.paragraph("#")])
    }

    @Test func numbersThatAreNotListMarksStayText() {
        #expect(ReplyText.blocks("2.5 kg more") == [.paragraph("2.5 kg more")])
        #expect(ReplyText.blocks(". done") == [.paragraph(". done")])
        #expect(ReplyText.blocks("a. first") == [.paragraph("a. first")])
    }

    @Test func halfArrivedMarkupShowsAsTypedUntilItCloses() {
        // Mid-stream: a list mark with nothing after it yet, and bold not yet closed.
        #expect(ReplyText.blocks("Plan:\n-") == [.paragraph("Plan: -")])
        #expect(ReplyText.blocks("Plan:\n1.") == [.paragraph("Plan: 1.")])
        #expect(ReplyText.blocks("Your **Bench") == [.paragraph("Your **Bench")])
        #expect(ReplyText.blocks("- **Bench** 60 kg\n- Sq") == [.list(["**Bench** 60 kg", "Sq"], numbered: false)])
    }

    @Test func nothingIsNoBlocks() {
        #expect(ReplyText.blocks("").isEmpty)
        #expect(ReplyText.blocks("\n\n  \n").isEmpty)
    }
}

/// What the conversation shows, row by row.
struct ConversationRowTests {
    private func card(_ kind: Milo_CardKind, _ id: String = UUID().uuidString) -> MiloSession.Item {
        var c = Milo_Card()
        c.id = id
        c.kind = kind
        return .card(c)
    }

    @Test func readingsInARowFoldIntoOneLine() {
        let items: [MiloSession.Item] = [
            .user(UUID(), "how was last week"),
            .reading(UUID(), "Read 3 workouts"),
            .reading(UUID(), "Read bench history"),
            .reply(UUID(), "Solid."),
            .reading(UUID(), "Read squat history"),
        ]
        let rows = Conversation.rows(items, busy: false)
        #expect(rows.count == 4)
        #expect(rows[1].readings == ["Read 3 workouts", "Read bench history"])
        #expect(rows[3].readings == ["Read squat history"])
    }

    @Test func onlyTheFirstLineAfterYouCarriesMilosName() {
        let items: [MiloSession.Item] = [
            .user(UUID(), "log it"),
            .reading(UUID(), "Read today"),
            .reply(UUID(), "Done."),
            card(.done),
            .user(UUID(), "thanks"),
            .reply(UUID(), "Any time."),
        ]
        #expect(Conversation.rows(items, busy: false).map(\.startsReply) == [true, true, false, false, false, true])
    }

    @Test func suggestionsShowOnlyUnderTheLatestFinishedAnswer() {
        let old = card(.suggestions, "old")
        let latest = card(.suggestions, "latest")
        let items: [MiloSession.Item] = [.user(UUID(), "hi"), .reply(UUID(), "Hi."), old, .user(UUID(), "and?"), .reply(UUID(), "More."), latest]
        #expect(Conversation.rows(items, busy: false).map(\.id).contains("latest"))
        #expect(!Conversation.rows(items, busy: false).map(\.id).contains("old"))
        // While Milo is still going, not even the latest.
        #expect(!Conversation.rows(items, busy: true).map(\.id).contains("latest"))
    }
}

/// Which layout each card kind gets in each state.
struct CardLayoutTests {
    private func card(_ kind: Milo_CardKind, lines: [String] = ["Weight"]) -> Milo_Card {
        var c = Milo_Card()
        c.kind = kind
        c.lines = lines.map { label in
            var l = Milo_CardLine()
            l.label = label
            return l
        }
        return c
    }

    @Test func doneCardsAlwaysFold() {
        for state in [MiloSession.CardState.pending, .applied, .undone] {
            #expect(CardView.layout(card(.done), state) == .banner)
        }
    }

    @Test func appliedCardsFoldExceptChartsAndWorkouts() {
        #expect(CardView.layout(card(.change), .applied) == .banner)
        #expect(CardView.layout(card(.newExercise), .applied) == .banner)
        #expect(CardView.layout(card(.templateDraft), .applied) == .banner)
        #expect(CardView.layout(card(.chart), .applied) == .full)
        #expect(CardView.layout(card(.workout), .applied) == .full)
    }

    @Test func aWorkoutRenameIsItsOwnSmallCard() {
        #expect(CardView.layout(card(.change, lines: ["Workout name"]), .pending) == .rename)
        #expect(CardView.layout(card(.change, lines: ["Workout name"]), .dismissed) == .rename)
        // Renaming along with other changes is an ordinary change card.
        #expect(CardView.layout(card(.change, lines: ["Workout name", "Notes"]), .pending) == .full)
        #expect(CardView.layout(card(.change, lines: ["Weight"]), .pending) == .full)
    }

    @Test func pendingStaleAndDismissedCardsShowInFull() {
        for kind in [Milo_CardKind.change, .newExercise, .templateDraft, .progress, .breakdown, .chart] {
            for state in [MiloSession.CardState.pending, .stale, .dismissed] {
                #expect(CardView.layout(card(kind), state) == .full)
            }
        }
    }
}

/// Earlier conversations under Chats, grouped by when.
struct ChatGroupTests {
    @Test func groupsByDayThenMonth() {
        var calendar = Calendar(identifier: .gregorian)
        calendar.timeZone = TimeZone(identifier: "UTC")!
        let now = Date(timeIntervalSince1970: 1_794_300_000) // Tuesday 10 November 2026
        func chat(_ title: String, daysAgo: Double) -> Milo_Conversation {
            var c = Milo_Conversation()
            c.id = title
            c.title = title
            c.updatedAt = .init(date: now.addingTimeInterval(-daysAgo * 86400))
            return c
        }
        let groups = ChatGroup.groups(
            [chat("old", daysAgo: 40), chat("today", daysAgo: 0.1), chat("yesterday", daysAgo: 1), chat("monday", daysAgo: 4), chat("also today", daysAgo: 0.2)],
            now: now, calendar: calendar)
        #expect(groups.map(\.title) == ["Today", "Yesterday", "Earlier this week", "October 2026"])
        #expect(groups[0].chats.map(\.title) == ["today", "also today"])
        #expect(groups[0].showsTime && !groups[2].showsTime)
    }
}
