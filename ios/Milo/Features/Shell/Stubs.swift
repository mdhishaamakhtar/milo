import SwiftUI

// Screens still being built.
struct HistoryView: View { var body: some View { Text("History").miloScreen() } }
struct ProgressTab: View { var body: some View { Text("Progress").miloScreen() } }
struct ProfileView: View { var body: some View { Text("Profile").miloScreen() } }
struct WorkoutDetailView: View { let workoutID: UUID; var body: some View { Text("Workout").miloScreen() } }
struct TemplateEditor: View { let template: Template; var body: some View { Text("Template").miloScreen() } }
struct ExerciseDetailView: View { let exerciseID: UUID; var body: some View { Text("Exercise").miloScreen() } }
