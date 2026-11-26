# Native module avkit

Scope: `internal/avkit/`. Source paths below are relative to that directory.

avkit is this app's macOS MyGo native module, implemented in Swift.
Windows playback uses `internal/playerengine` and MyGo `ui/video` instead.
Read MyGo's `docs/native-modules.md` before changing the module contract.

- `avkit.go` declares the `Player` interface and `Status` snapshot: it is
  the contract. `//mygo:native` marks `Player` as an entry point and
  generates its `NewPlayer` constructor.
- `avkit_native.go` and `swift/Sources/Avkit/Avkit.mygo.swift`
  are generated: never edit them; `mygo dev`
  regenerates them when the interfaces change.
- `swift/` is the macOS Swift package for Apple's media frameworks:
  `makePlayer()` implements the `Player` entry point.
  Add Swift packages and frameworks in `swift/Package.swift`.

Check the module from the repository root without opening the app:

```sh
go run github.com/egoist/mygo/cmd/mygo native check ./internal/avkit
```
