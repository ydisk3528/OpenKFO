package protocol

// Native handlers and exact layouts: reference layouts.py at e2c61c4.
const (
	BattleEventReborn           uint32 = 8157
	BattleEventItemComplete     uint32 = 8276 // 828CF0: finish the picked-up object's action.
	BattleEventDeathCountdown   uint32 = 8278 // 828EB0: remote owner UI only.
	BattleEventDeathTerminal    uint32 = 8286 // 8285E0: self or controller.
	BattleEventActionArgument   uint32 = 8125 // 828120
	BattleEventTargetSelection  uint32 = 8143 // 8283D0
	BattleEventPairTransform    uint32 = 8144 // 82B840
	BattleEventSlip             uint32 = 8270 // 828BF0
	BattleEventTargetAction     uint32 = 8280 // 8281D0
	BattleEventWeaponOperation  uint32 = 8284 // 82B010
	BattleEventCollectibleSpawn uint32 = 8287 // 8298D0
	BattleEventActorValue       uint32 = 8293 // 82AD60
	BattleEventProjectileCreate uint32 = 8400
	BattleEventProjectileResult uint32 = 8401
	BattleEventProjectileHit    uint32 = 8402
	BattleEventProjectileUpdate uint32 = 8403
	BattleEventProjectileRemove uint32 = 8404
)
