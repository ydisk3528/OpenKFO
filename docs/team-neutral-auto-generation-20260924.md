# Team NPC server-coordinated local experiment, 2026-09-24

Implemented owner-authenticated 21901 request and frozen per-round spawn plan (NPC 100 / M_NPC_EGUN / shared position / fighter snapshot). 21900 receipts now require an existing plan. Duplicate requests do not reset positions/deadlines; unsolicited/stale/late acknowledgements cannot activate NPC authority. Pending plans cancel on failure, roster/host change, or 45-second timeout. Ordinary departure after activation retains the remaining replica authority; owner changes revoke it.

Loopback-only NPC2 plan endpoint returns fixed 192-byte data with per-UID states. A loopback-only failure endpoint permits cancellation only, never spawn authorization or success receipts. Existing NPC1 status remains available for AI guard. All routes remain behind ExperimentalNeutralNPC and loopback-listener validation.

Standalone EXE sources: E:\功夫小子\keji\KK_Science_Exe\team_auto.h, team_mod.h, main.cpp. One owner action requests the plan. Coordinator discovers exact-hash local gfxz processes matching room/serial/UID, preflights the full participant list, then starts hidden one-shot instances of itself. Each independently reads and validates the server plan before generating a passive NPC with frozen parameters and sending the authenticated ready receipt. Per-round and per-PID mutexes prevent concurrent dispatch. AI remains an explicit separate action. No automatic generation was executed during development; user must validate live gameplay.

Build/test: go test ./internal/game ./cmd/server; C++ build.cmd; --self-test (includes NPC2 malformed plan checks); --ui-self-test. Passed.
Local deployed server SHA256 3D233796DC11A5C78C33EA0677740A72186451587C9C23DC4305269DB793D815; PID 98708 at deployment; health OK, ports 19090/19091 loopback, plan route 409 for absent battle (enabled).
Tool SHA256 C2F1330D210C8FC3F6F5FC0825B1BF6FF8701126C2C8D9A062E1184484225F74.
No production changes, no database schema changes. Runtime backup retained in runtime-local/team-mod-auto-20260924.

Limitations: local machine participants only; cancellation blocks AI rather than destroying live native objects; passive replicas clear with scene teardown. Real two-client spawning, movement, health and death remain to be accepted in-game. Native generation still shares existing experimental callback/SEH protections and is not a crash-free guarantee.
