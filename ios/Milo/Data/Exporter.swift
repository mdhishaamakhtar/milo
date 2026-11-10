import Foundation
import GRDB

/// Everything you've logged, as files you keep: JSON with all of it, and a CSV with
/// one row per set for spreadsheets.
nonisolated enum Exporter {
    struct Files: Sendable {
        let json: URL
        let csv: URL
    }

    static func write(_ db: AppDatabase, to dir: URL = FileManager.default.temporaryDirectory) throws -> Files {
        let export = try db.writer.read { try build($0, db) }
        let day = Date.now.formatted(.iso8601.year().month().day())
        let json = dir.appending(path: "milo-export-\(day).json")
        let csv = dir.appending(path: "milo-sets-\(day).csv")

        let encoder = JSONEncoder()
        encoder.outputFormatting = [.prettyPrinted, .sortedKeys]
        encoder.keyEncodingStrategy = .convertToSnakeCase
        encoder.dateEncodingStrategy = .iso8601
        try encoder.encode(export).write(to: json, options: .atomic)
        try Data(rows(export).utf8).write(to: csv, options: .atomic)
        return Files(json: json, csv: csv)
    }

    // MARK: The JSON

    struct Export: Encodable {
        var exportedAt = Date.now
        var displayUnit: String
        var workouts: [WorkoutOut]
        var templates: [TemplateOut]
        var body: [BodyOut]
        var yourExercises: [ExerciseOut]
    }

    struct WorkoutOut: Encodable {
        var name: String
        var startedAt: Date
        var endedAt: Date?
        var notes: String
        var exercises: [EntryOut]
    }

    struct EntryOut: Encodable {
        var exercise: String
        var superset: String?
        var notes: String
        var sets: [SetOut]
    }

    struct SetOut: Encodable {
        var weight: String?
        var unit: String?
        var reps: Int?
        var seconds: Int?
        var metres: Double?
        var eachSide: Bool
        var done: Bool
    }

    struct TemplateOut: Encodable {
        var name: String
        var exercises: [String]
    }

    struct BodyOut: Encodable {
        var measuredAt: Date
        var weightKg: Double?
        var bodyFatPct: Double?
        var source: String
    }

    struct ExerciseOut: Encodable {
        var name: String
        var equipment: String
        var measurement: String
        var oneSideAtATime: Bool
        var muscles: [String]
    }

    private static func build(_ d: Database, _ db: AppDatabase) throws -> Export {
        let workouts = try Workout.filter(Column("deleted") == false && Column("endedAt") != nil).order(Column("startedAt")).fetchAll(d)
        let names = Dictionary(uniqueKeysWithValues: try Exercise.fetchAll(d).map { ($0.id, $0.name) })
        return Export(
            displayUnit: try db.settings(d).displayUnit.rawValue,
            workouts: try workouts.compactMap { w in
                guard let detail = try db.workoutDetail(d, id: w.id) else { return nil }
                return WorkoutOut(name: w.name, startedAt: w.startedAt, endedAt: w.endedAt, notes: w.notes, exercises: detail.entries.map { e in
                    EntryOut(exercise: e.exercise.name, superset: e.we.groupLabel.isEmpty ? nil : e.we.groupLabel, notes: e.we.notes, sets: e.sets.map { s in
                        SetOut(weight: s.weight.isEmpty ? nil : s.weight, unit: s.weight.isEmpty ? nil : s.unit.rawValue, reps: s.reps,
                               seconds: s.durationS, metres: s.distanceM, eachSide: s.perSide, done: s.done)
                    })
                })
            },
            templates: try Template.filter(Column("deleted") == false).fetchAll(d).map { t in
                TemplateOut(name: t.name, exercises: t.items.map { names[$0.exerciseId] ?? "Exercise" })
            },
            body: try BodyMeasurement.filter(Column("deleted") == false).order(Column("measuredAt")).fetchAll(d).map {
                BodyOut(measuredAt: $0.measuredAt, weightKg: $0.weightKg, bodyFatPct: $0.bodyFatPct, source: $0.source)
            },
            yourExercises: try Exercise.filter(Column("builtin") == false && Column("deleted") == false).fetchAll(d).map {
                ExerciseOut(name: $0.name, equipment: $0.equipment, measurement: $0.measurement.rawValue, oneSideAtATime: $0.unilateral, muscles: $0.primaryMuscles + $0.secondaryMuscles)
            })
    }

    // MARK: The CSV

    /// One row per done set: date, workout, exercise, set, weight, unit, reps, seconds, metres, each side.
    static func rows(_ export: Export) -> String {
        var lines = ["date,workout,exercise,set,weight,unit,reps,seconds,metres,each_side"]
        for w in export.workouts {
            let date = w.startedAt.formatted(.iso8601.year().month().day())
            for e in w.exercises {
                for (i, s) in e.sets.filter(\.done).enumerated() {
                    lines.append([date, w.name, e.exercise, "\(i + 1)", s.weight ?? "", s.unit ?? "", s.reps.map(String.init) ?? "",
                                  s.seconds.map(String.init) ?? "", s.metres.map { Format.trim(String($0)) } ?? "", s.eachSide ? "yes" : ""]
                        .map(field).joined(separator: ","))
                }
            }
        }
        return lines.joined(separator: "\n") + "\n"
    }

    private static func field(_ s: String) -> String {
        s.contains(where: { $0 == "," || $0 == "\"" || $0 == "\n" }) ? "\"" + s.replacingOccurrences(of: "\"", with: "\"\"") + "\"" : s
    }
}
