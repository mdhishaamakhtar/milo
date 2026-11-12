import Testing
@testable import Milo

struct ReturnKeyTests {
    @Test func aTypedReturnIsOneLineBreakAdded() {
        #expect(returnTyped(old: "push", new: "push\n"))
        #expect(returnTyped(old: "", new: "\n"))
        #expect(returnTyped(old: "pushday", new: "push\nday"))
    }

    @Test func pastedOrTypedTextIsNotAReturn() {
        #expect(!returnTyped(old: "push", new: "pushd"))
        #expect(!returnTyped(old: "", new: "a\nb"))
        #expect(!returnTyped(old: "push", new: "pus"))
        #expect(!returnTyped(old: "a\nb", new: "a\nbc"))
    }
}
