# Real-world `.gdextension` fixtures

Every file in this directory is a byte-for-byte copy of a `.gdextension` shipped by a
published GDExtension addon. They exist so partition and reassembly are exercised against
the shapes real producers emit rather than only against hand-written input. Fetching them
was a one-time step: the tests read these checked-in bytes and never reach the network.

## `terrabrush.gdextension`

- Addon: TerraBrush, a Godot terrain editing addon published on the Godot Asset Library.
- Upstream: <https://github.com/spimort/TerraBrush>
- Path in that repository: `demo/addons/terrabrush/terrabrush.gdextension`
- Pinned at commit `4f68fe4f6a1b330fa023b57fe50586dd756fff0f` (`main`, nearest tag `0.9.4Alpha`),
  retrieved from
  <https://raw.githubusercontent.com/spimort/TerraBrush/4f68fe4f6a1b330fa023b57fe50586dd756fff0f/demo/addons/terrabrush/terrabrush.gdextension>
- SHA-256: `adb284707852aa9993875856c56608d1af54bda76eb733d44e75fd9de7a46266`
- Why this one: its `[libraries]` values are `res://` paths under a single addon root, it
  carries an author-written `[icons]` section between `[configuration]` and `[libraries]`
  that partition must preserve verbatim, and its platform tags cover six platforms.

The sibling fixtures one directory up — `limboai.gdextension` and `godot_jolt.gdextension` —
are also verbatim copies of published addons and are reused rather than duplicated here.

`limboai.gdextension` ships a Godot dictionary as its `[dependencies]` value, mapping each
dependency's `res://` path to the export subdirectory Godot copies it into, which is the
shape `loreline`, `sentry-godot` and `godot-sqlite` ship too. It is **accepted**: the index
stores a dependency entry as that same dictionary, so the destinations survive the partition,
the index, and reassembly. It is the real-world evidence for the dictionary rows of the
fail-closed contract and the fixture the round trip is asserted over.

`godot_jolt.gdextension` ships all fourteen of its `[libraries]` values relative to the
`.gdextension` instead of as `res://` paths, which is the other form Godot accepts and the only
one this addon uses. It is **accepted**: each value is resolved against the `.gdextension` file's
own position inside the addon, so `bin/godot-jolt_macos.framework` becomes
`res://addons/godot_jolt/bin/godot-jolt_macos.framework`, and `res://` is the single form the
index publishes and reassembly emits. It is the real-world evidence for the relative-value rows of
the fail-closed contract, and the fixture whose round trip is asserted as semantic rather than
byte-identical, because the author's spelling is canonicalized on the way into the index.

It is also the real-world evidence for two shapes the grouping rules exist for: its three Windows
targets name one file, and `macos.editor` sits beside `macos.template_release.universal`, so one
platform's entries land under both its generic slice and an architecture-specific one.
