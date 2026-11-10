import Foundation
import MiloAPI
import SwiftProtobuf

// The one place local records and API messages convert.

nonisolated enum Proto {
    /// A change from the server or a Milo card as a local record (not dirty).
    static func record(_ c: Milo_Change) -> (any SyncedRecord)? {
        switch c.row {
        case .exercise(let e)?: Exercise(e)
        case .exercisePref(let p)?: ExercisePref(p)
        case .workout(let w)?: Workout(w)
        case .workoutExercise(let we)?: WorkoutExercise(we)
        case .set(let s)?: LoggedSet(s)
        case .template(let t)?: Template(t)
        case .bodyMeasurement(let b)?: BodyMeasurement(b)
        case .settings(let s)?: UserSettings(s)
        case nil: nil
        }
    }

    static func id(_ uuid: UUID) -> String { uuid.uuidString.lowercased() }
    static func time(_ date: Date) -> Google_Protobuf_Timestamp { Google_Protobuf_Timestamp(date: date) }
}

nonisolated extension UUID {
    init?(protoID s: String) {
        guard !s.isEmpty else { return nil }
        self.init(uuidString: s)
    }
}

nonisolated extension MeasurementKind {
    init(_ m: Milo_Measurement) {
        self = switch m {
        case .bodyweight: .bodyweight
        case .assisted: .assisted
        case .timed: .timed
        case .distance: .distance
        default: .repsLoad
        }
    }

    var proto: Milo_Measurement {
        switch self {
        case .repsLoad: .repsLoad
        case .bodyweight: .bodyweight
        case .assisted: .assisted
        case .timed: .timed
        case .distance: .distance
        }
    }
}

nonisolated extension WeightUnit {
    init(_ u: Milo_WeightUnit) { self = u == .lb ? .lb : .kg }
    var proto: Milo_WeightUnit { self == .lb ? .lb : .kg }
}

nonisolated extension SetKind {
    init(_ k: Milo_SetKind) {
        self = switch k {
        case .warmup: .warmup
        case .drop: .drop
        case .failure: .failure
        default: .normal
        }
    }

    var proto: Milo_SetKind {
        switch self {
        case .normal: .normal
        case .warmup: .warmup
        case .drop: .drop
        case .failure: .failure
        }
    }
}

nonisolated extension Exercise {
    init?(_ e: Milo_Exercise) {
        guard let id = UUID(protoID: e.id) else { return nil }
        self.init(
            id: id, builtin: e.builtin, name: e.name, aliases: e.aliases, primaryMuscles: e.primaryMuscles,
            secondaryMuscles: e.secondaryMuscles, movementPattern: e.movementPattern, equipment: e.equipment,
            measurement: MeasurementKind(e.measurement), unilateral: e.unilateral, met: e.hasMet ? e.met : nil,
            about: e.description_p, approximate: e.approximate, updatedAt: e.updatedAt.date, deleted: e.deleted, dirty: false)
    }

    var change: Milo_Change {
        var e = Milo_Exercise()
        e.id = Proto.id(id); e.builtin = builtin; e.name = name; e.aliases = aliases
        e.primaryMuscles = primaryMuscles; e.secondaryMuscles = secondaryMuscles
        e.movementPattern = movementPattern; e.equipment = equipment; e.measurement = measurement.proto
        e.unilateral = unilateral; if let met { e.met = met }
        e.description_p = about; e.approximate = approximate; e.updatedAt = Proto.time(updatedAt); e.deleted = deleted
        var c = Milo_Change(); c.exercise = e; return c
    }
}

nonisolated extension ExercisePref {
    init?(_ p: Milo_ExercisePref) {
        guard let id = UUID(protoID: p.exerciseID) else { return nil }
        self.init(exerciseId: id, unit: WeightUnit(p.unit), restS: p.hasRestS ? Int(p.restS) : nil, updatedAt: p.updatedAt.date, deleted: p.deleted, dirty: false)
    }

    var change: Milo_Change {
        var p = Milo_ExercisePref()
        p.exerciseID = Proto.id(exerciseId); p.unit = unit.proto; if let restS { p.restS = Int32(restS) }
        p.updatedAt = Proto.time(updatedAt); p.deleted = deleted
        var c = Milo_Change(); c.exercisePref = p; return c
    }
}

nonisolated extension Workout {
    init?(_ w: Milo_Workout) {
        guard let id = UUID(protoID: w.id) else { return nil }
        self.init(
            id: id, name: w.name, notes: w.notes, startedAt: w.startedAt.date, endedAt: w.hasEndedAt ? w.endedAt.date : nil,
            templateId: UUID(protoID: w.templateID), healthUUID: UUID(protoID: w.healthUuid), updatedAt: w.updatedAt.date, deleted: w.deleted, dirty: false)
    }

    var change: Milo_Change {
        var w = Milo_Workout()
        w.id = Proto.id(id); w.name = name; w.notes = notes; w.startedAt = Proto.time(startedAt)
        if let endedAt { w.endedAt = Proto.time(endedAt) }
        w.templateID = templateId.map(Proto.id) ?? ""; w.healthUuid = healthUUID.map(Proto.id) ?? ""
        w.updatedAt = Proto.time(updatedAt); w.deleted = deleted
        var c = Milo_Change(); c.workout = w; return c
    }
}

nonisolated extension WorkoutExercise {
    init?(_ we: Milo_WorkoutExercise) {
        guard let id = UUID(protoID: we.id), let workout = UUID(protoID: we.workoutID), let exercise = UUID(protoID: we.exerciseID) else { return nil }
        self.init(id: id, workoutId: workout, exerciseId: exercise, position: Int(we.position), groupLabel: we.groupLabel, notes: we.notes, updatedAt: we.updatedAt.date, deleted: we.deleted, dirty: false)
    }

    var change: Milo_Change {
        var we = Milo_WorkoutExercise()
        we.id = Proto.id(id); we.workoutID = Proto.id(workoutId); we.exerciseID = Proto.id(exerciseId)
        we.position = Int32(position); we.groupLabel = groupLabel; we.notes = notes
        we.updatedAt = Proto.time(updatedAt); we.deleted = deleted
        var c = Milo_Change(); c.workoutExercise = we; return c
    }
}

nonisolated extension LoggedSet {
    init?(_ s: Milo_Set) {
        guard let id = UUID(protoID: s.id), let we = UUID(protoID: s.workoutExerciseID) else { return nil }
        self.init(
            id: id, workoutExerciseId: we, position: Int(s.position), kind: SetKind(s.kind), reps: s.hasReps ? Int(s.reps) : nil,
            weight: s.weight, unit: WeightUnit(s.weightUnit), perSide: s.perSide, durationS: s.hasDurationS ? Int(s.durationS) : nil,
            distanceM: s.hasDistanceM ? s.distanceM : nil, rpe: s.hasRpe ? s.rpe : nil, done: s.done,
            updatedAt: s.updatedAt.date, deleted: s.deleted, dirty: false)
    }

    var change: Milo_Change {
        var s = Milo_Set()
        s.id = Proto.id(id); s.workoutExerciseID = Proto.id(workoutExerciseId); s.position = Int32(position); s.kind = kind.proto
        if let reps { s.reps = Int32(reps) }
        s.weight = weight; s.weightUnit = unit.proto; s.perSide = perSide
        if let durationS { s.durationS = Int32(durationS) }
        if let distanceM { s.distanceM = distanceM }
        if let rpe { s.rpe = rpe }
        s.done = done; s.updatedAt = Proto.time(updatedAt); s.deleted = deleted
        var c = Milo_Change(); c.set = s; return c
    }
}

nonisolated extension Template {
    init?(_ t: Milo_Template) {
        guard let id = UUID(protoID: t.id) else { return nil }
        let items = t.items.compactMap { i in UUID(protoID: i.exerciseID).map { TemplateItem(exerciseId: $0, groupLabel: i.groupLabel) } }
        self.init(id: id, name: t.name, notes: t.notes, items: items, updatedAt: t.updatedAt.date, deleted: t.deleted, dirty: false)
    }

    var change: Milo_Change {
        var t = Milo_Template()
        t.id = Proto.id(id); t.name = name; t.notes = notes
        t.items = items.map { item in
            var i = Milo_TemplateItem()
            i.exerciseID = Proto.id(item.exerciseId); i.groupLabel = item.groupLabel
            return i
        }
        t.updatedAt = Proto.time(updatedAt); t.deleted = deleted
        var c = Milo_Change(); c.template = t; return c
    }
}

nonisolated extension BodyMeasurement {
    init?(_ b: Milo_BodyMeasurement) {
        guard let id = UUID(protoID: b.id) else { return nil }
        self.init(id: id, measuredAt: b.measuredAt.date, weightKg: b.hasWeightKg ? b.weightKg : nil, bodyFatPct: b.hasBodyFatPct ? b.bodyFatPct : nil, source: b.source, updatedAt: b.updatedAt.date, deleted: b.deleted, dirty: false)
    }

    var change: Milo_Change {
        var b = Milo_BodyMeasurement()
        b.id = Proto.id(id); b.measuredAt = Proto.time(measuredAt)
        if let weightKg { b.weightKg = weightKg }
        if let bodyFatPct { b.bodyFatPct = bodyFatPct }
        b.source = source; b.updatedAt = Proto.time(updatedAt); b.deleted = deleted
        var c = Milo_Change(); c.bodyMeasurement = b; return c
    }
}

nonisolated extension UserSettings {
    init(_ s: Milo_Settings) {
        self.init(displayUnit: WeightUnit(s.displayUnit), restDefaultS: Int(s.restDefaultS), heightCm: s.hasHeightCm ? s.heightCm : nil, birthDate: s.birthDate, sex: s.sex, updatedAt: s.updatedAt.date, dirty: false)
    }

    var change: Milo_Change {
        var s = Milo_Settings()
        s.displayUnit = displayUnit.proto; s.restDefaultS = Int32(restDefaultS)
        if let heightCm { s.heightCm = heightCm }
        s.birthDate = birthDate; s.sex = sex; s.updatedAt = Proto.time(updatedAt)
        var c = Milo_Change(); c.settings = s; return c
    }
}
