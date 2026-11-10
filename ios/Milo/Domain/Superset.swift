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
