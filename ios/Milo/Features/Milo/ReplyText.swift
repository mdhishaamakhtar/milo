import SwiftUI

/// Milo's reply, set from the little Markdown it writes: paragraphs, "- " and "1." lists, and
/// **bold** for exercise names and the number that matters. Replies arrive a few words at a time,
/// so half-finished markup shows as typed until it closes.
struct ReplyText: View {
    let text: String

    var body: some View {
        VStack(alignment: .leading, spacing: 12) {
            ForEach(Array(Self.blocks(text).enumerated()), id: \.offset) { _, block in
                switch block {
                case .paragraph(let line):
                    Self.line(line)
                case .list(let items, let numbered):
                    VStack(alignment: .leading, spacing: 10) {
                        ForEach(Array(items.enumerated()), id: \.offset) { i, item in
                            HStack(alignment: .firstTextBaseline, spacing: 9) {
                                Text(numbered ? "\(i + 1)." : "•")
                                    .font(.sofia(16.5, numbered ? .semibold : .bold))
                                    .foregroundStyle(Color.tx3)
                                    .monospacedDigit()
                                    .frame(minWidth: numbered ? 16 : 8, alignment: .leading)
                                Self.line(item)
                            }
                        }
                    }
                }
            }
        }
        .padding(.horizontal, 4)
    }

    private static func line(_ markdown: String) -> some View {
        Text(inline(markdown))
            .font(.sofia(16.5))
            .lineSpacing(4)
            .frame(maxWidth: .infinity, alignment: .leading)
            .fixedSize(horizontal: false, vertical: true)
    }

    private static func inline(_ markdown: String) -> AttributedString {
        (try? AttributedString(markdown: markdown, options: .init(interpretedSyntax: .inlineOnlyPreservingWhitespace)))
            ?? AttributedString(markdown)
    }

    enum Block: Equatable {
        case paragraph(String)
        case list([String], numbered: Bool)
    }

    /// Paragraphs split on blank lines; consecutive "- " or "1. " lines become one list.
    /// A heading, which Milo shouldn't write, becomes a bold line.
    static func blocks(_ text: String) -> [Block] {
        var out: [Block] = []
        var paragraph: [String] = []
        var items: [String] = []
        var numbered = false

        func endParagraph() {
            if !paragraph.isEmpty { out.append(.paragraph(paragraph.joined(separator: " "))) }
            paragraph = []
        }
        func endList() {
            if !items.isEmpty { out.append(.list(items, numbered: numbered)) }
            items = []
        }

        for raw in text.components(separatedBy: "\n") {
            let line = raw.trimmingCharacters(in: .whitespaces)
            if line.isEmpty {
                endParagraph(); endList()
            } else if let item = bullet(line) {
                endParagraph()
                if numbered { endList() }
                numbered = false
                items.append(item)
            } else if let item = numberedItem(line) {
                endParagraph()
                if !numbered { endList() }
                numbered = true
                items.append(item)
            } else {
                endList()
                if let heading = heading(line) {
                    endParagraph()
                    out.append(.paragraph("**\(heading)**"))
                } else {
                    paragraph.append(line)
                }
            }
        }
        endParagraph(); endList()
        return out
    }

    private static func bullet(_ line: String) -> String? {
        for mark in ["- ", "* ", "• "] where line.hasPrefix(mark) {
            return String(line.dropFirst(mark.count))
        }
        return nil
    }

    private static func numberedItem(_ line: String) -> String? {
        guard let dot = line.firstIndex(of: "."), line[..<dot].allSatisfy(\.isNumber), !line[..<dot].isEmpty else { return nil }
        let rest = line[line.index(after: dot)...]
        guard rest.first == " " else { return nil }
        return String(rest.dropFirst())
    }

    private static func heading(_ line: String) -> String? {
        guard line.hasPrefix("#") else { return nil }
        let text = line.drop(while: { $0 == "#" }).trimmingCharacters(in: .whitespaces)
        return text.isEmpty ? nil : text
    }
}
