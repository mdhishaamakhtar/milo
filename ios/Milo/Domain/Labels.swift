import Foundation

nonisolated enum Superset {
    /// Numbers for a list of exercises, with neighbours in the same group lettered:
    /// ["", "", "g", "g"] → ["1", "2", "3a", "3b"].
    static func labels(_ groups: [String]) -> [String] {
        var out: [String] = []
        var number = 0
        var i = 0
        while i < groups.count {
            var j = i + 1
            while !groups[i].isEmpty, j < groups.count, groups[j] == groups[i] { j += 1 }
            number += 1
            if j - i > 1 {
                for k in 0..<(j - i) { out.append("\(number)\(Character(UnicodeScalar(UInt8(97 + k))))") }
            } else {
                out.append("\(number)")
            }
            i = j
        }
        return out
    }
}

/// What stands in a set's number column: ordinary sets are numbered 1, 2, 3; a
/// warm-up is W, a drop set D and a set to failure F, and none of those takes a number.
nonisolated struct SetMark: Equatable, Sendable {
    let text: String
    let isLetter: Bool

    static func marks(_ kinds: [SetKind]) -> [SetMark] {
        var number = 0
        return kinds.map { kind in
            if kind == .normal { number += 1 }
            return switch kind {
            case .normal: SetMark(text: "\(number)", isLetter: false)
            case .warmup: SetMark(text: "W", isLetter: true)
            case .drop: SetMark(text: "D", isLetter: true)
            case .failure: SetMark(text: "F", isLetter: true)
            }
        }
    }
}

nonisolated extension SetKind {
    var title: String {
        switch self {
        case .normal: "Normal set"
        case .warmup: "Warm-up"
        case .drop: "Drop set"
        case .failure: "To failure"
        }
    }
}
