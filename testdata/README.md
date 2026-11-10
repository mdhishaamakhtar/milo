# Shared test vectors

Numbers every client must compute the same way (units, volume, estimated 1RM, calories, new bests).
Each file is a JSON array of `{ "name", "input", "expected" }` cases. The Go server's tests and the
iOS app's tests both read these files; an Android client should too.
