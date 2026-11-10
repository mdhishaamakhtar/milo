import SwiftUI

/// The one field at the bottom of a workout: type a name and it matches your
/// exercises; describe something new and Milo works it out.
struct AddField: View {
    @Environment(WorkoutModel.self) private var model
    var placeholder: String
    var focused: FocusState<Bool>.Binding
    let ask: (String) -> Void

    var body: some View {
        @Bindable var model = model
        HStack(spacing: 11) {
            BarMLogo(size: 18)
            TextField(placeholder, text: $model.query, axis: .vertical)
                .font(.sofia(16.5, .regular))
                .lineLimit(1...4)
                .focused(focused)
                .submitLabel(.send)
                .onSubmit(send)
            if model.query.trimmingCharacters(in: .whitespaces).isEmpty {
                Image(systemName: "mic").font(.system(size: 17, weight: .medium)).foregroundStyle(Color.tx2).frame(width: 38, height: 38)
            } else {
                Button(action: send) {
                    Image(systemName: "arrow.up").font(.system(size: 15, weight: .bold)).foregroundStyle(Color.bg)
                        .frame(width: 38, height: 38).background(Color.tx, in: .circle)
                }
                .buttonStyle(.plain)
                .accessibilityLabel("Send to Milo")
            }
        }
        .padding(.leading, 17)
        .padding(.trailing, 7)
        .padding(.vertical, 7)
        .glassEffect(.regular.interactive(), in: .rect(cornerRadius: 26))
    }

    private func send() {
        let text = model.query.trimmingCharacters(in: .whitespacesAndNewlines)
        guard !text.isEmpty else { return }
        model.query = ""
        ask(text)
    }
}

/// Exercises matching what's typed, and the way to hand it to Milo instead.
struct MatchList: View {
    @Environment(AppModel.self) private var app
    @Environment(WorkoutModel.self) private var model
    @State private var matches = Live<[ExerciseMatch]>([])
    let ask: (String) -> Void

    var body: some View {
        VStack(alignment: .leading, spacing: 10) {
            if !matches.value.isEmpty {
                Text("\(matches.value.count) in your exercises").font(.cap).foregroundStyle(Color.tx3).padding(.horizontal, 4)
                VStack(spacing: 0) {
                    ForEach(Array(matches.value.enumerated()), id: \.element.id) { i, match in
                        if i > 0 { Divider().overlay(Color.line).padding(.leading, 16) }
                        Button { model.add(match.exercise) } label: {
                            HStack {
                                VStack(alignment: .leading, spacing: 2) {
                                    Text(highlighted(match.exercise.name)).font(.sofia(16, .regular)).lineLimit(1)
                                    Text(match.lastDate.map { "Last \(Format.shortDay($0))" + (match.lastText.map { ": \($0)" } ?? "") } ?? match.exercise.equipmentLabel)
                                        .font(.sofia(14, .regular)).foregroundStyle(Color.tx2).lineLimit(1)
                                }
                                Spacer()
                                Image(systemName: "plus").font(.system(size: 15, weight: .semibold))
                                    .frame(width: 34, height: 34).background(Color.s2, in: .circle)
                            }
                            .padding(.horizontal, 16).frame(minHeight: 58).contentShape(.rect)
                        }
                        .buttonStyle(.plain)
                    }
                }
                .background(Color.s1, in: .rect(cornerRadius: Radius.card))
                .overlay(RoundedRectangle(cornerRadius: Radius.card).strokeBorder(Color.line))
            }
            Button { ask(model.query); model.query = "" } label: {
                HStack(alignment: .top, spacing: 12) {
                    BarMLogo(size: 18).padding(.top, 3)
                    VStack(alignment: .leading, spacing: 2) {
                        Text(matches.value.isEmpty ? "Not in your exercises" : "Not one of these?").font(.sofia(16, .semibold))
                        Text("Keep typing what it looks like. Milo will work it out.").font(.sofia(14, .regular)).foregroundStyle(Color.tx2)
                    }
                    Spacer(minLength: 0)
                }
                .padding(16)
                .background(Color.s1, in: .rect(cornerRadius: 20))
                .overlay(RoundedRectangle(cornerRadius: 20).strokeBorder(Color.line))
            }
            .buttonStyle(.plain)
        }
        .onChange(of: model.query, initial: true) { _, text in
            let db = app.db
            matches.watch(db) { try db.search($0, text, limit: 4) }
        }
    }

    /// The typed words in bold.
    private func highlighted(_ name: String) -> AttributedString {
        var s = AttributedString(name)
        s.foregroundColor = Color.tx2
        for word in model.query.lowercased().split(separator: " ") {
            if let r = s.range(of: word, options: .caseInsensitive) {
                s[r].foregroundColor = Color.tx
                s[r].font = .sofia(16, .bold)
            }
        }
        return s
    }
}
