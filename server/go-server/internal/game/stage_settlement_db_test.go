package game

import (
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
)

func TestStageSettlementRouteLocalDatabase(t *testing.T) {
	dsn := os.Getenv("OPENKFO_DEBUG_DSN")
	if dsn == "" {
		t.Skip("independent debug DB required")
	}
	cfg, err := mysql.ParseDSN(dsn)
	if err != nil || !strings.HasPrefix(cfg.DBName, "openkfo_debug_") {
		t.Fatal("not debug database")
	}
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	exec := func(q string, args ...any) {
		t.Helper()
		if _, err := db.Exec(q, args...); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`CREATE TEMPORARY TABLE stage_access(id INT PRIMARY KEY,revision BIGINT,rules MEDIUMBLOB) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE accounts(uid BIGINT PRIMARY KEY,profile BLOB,gold INT,tickets INT) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE counters(name VARCHAR(32) PRIMARY KEY,value BIGINT) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE battle_settlements(serial INT PRIMARY KEY,reports BLOB,result BLOB) ENGINE=InnoDB`,
		`CREATE TEMPORARY TABLE battle_reward_rules(id INT PRIMARY KEY,revision BIGINT,rules BLOB) ENGINE=InnoDB`,
	} {
		exec(q)
	}
	h, owner, peer, outsider := combatFixture()
	r := owner.Room
	h.Store = &persistence.Store{DB: db}
	r.Request[46] = byte(protocol.StageAssault)
	r.Serial = 1
	protocol.WriteUint32(r.Request, protocol.RoomMapOffset, 20051)
	r.StageWaves, _ = newStageWaves([]StageWavePlan{{Monsters: map[uint32]uint32{7: 1}}})
	r.StageWaves.finished = true
	r.StageWaves.index = 1
	r.BattleStartedAt = time.Now().Add(-125 * time.Second)
	h.Config.Settlement = persistence.RewardRules{StageRewards: []persistence.StageMapRewards{{MapID: 20051, Clear: persistence.StageReward{Experience: 20, RewardBundle: persistence.RewardBundle{Gold: 7, Tickets: 2}}}}}
	h.Config.ConfigHash = strings.Repeat("a", 64)
	access := persistence.StageAccess{ClientHash: h.Config.ConfigHash, PVEMaps: []uint32{20051}, Requirements: []persistence.StageTitleRequirement{{MapID: 20051, Name: "Test stage"}}, WavePlans: []persistence.StageWaveConfig{{MapID: 20051, ScriptHash: strings.Repeat("b", 64), RuntimeHash: strings.Repeat("c", 64), Templates: []string{"Monster"}, Variants: []StageWaveVariant{{MinPlayers: 1, MaxPlayers: 8, Waves: []StageWavePlan{{Monsters: map[uint32]uint32{0: 2}}}}}}}}
	accessData, err := json.Marshal(access)
	if err != nil {
		t.Fatal(err)
	}
	exec("INSERT INTO stage_access VALUES(1,1,?)", accessData)
	prepared, err := h.prepareStageBattle(r)
	if err != nil || prepared.plans[0].Monsters[0] != 2 {
		t.Fatal("stored wave plan not used", err)
	}
	if r.StageWaves.plans[0].Monsters[7] != 1 {
		t.Fatal("preparation modified active room")
	}
	rewards := h.Config.Settlement
	h.Config.Settlement = persistence.RewardRules{}
	if _, err = h.prepareStageBattle(r); err == nil {
		t.Fatal("missing stage rewards accepted")
	}
	h.Config.Settlement = rewards
	// Mode 10 reads its separate persisted plan, with the same reward gate.
	fosterAccess := access
	fosterAccess.WavePlans = nil
	fosterAccess.FosterPlans = []persistence.FosterConfig{{MapID: 20051, ScriptHash: h.Config.ConfigHash, RuntimeHash: h.Config.ConfigHash, ConfigHash: h.Config.ConfigHash, Templates: []string{"Monster"}, Plan: protocol.FosterPlan{InitialHP: []float32{8}, PlayerLimit: 6, GlobalLimit: 32, Groups: []protocol.FosterGroup{{SubLimit: 2, GroupLimit: 20, Spawns: []protocol.FosterSpawn{{Direction: 4}}}}}}}
	fosterData, err := json.Marshal(fosterAccess)
	if err != nil {
		t.Fatal(err)
	}
	exec("UPDATE stage_access SET rules=? WHERE id=1", fosterData)
	foster, err := h.prepareFosterBattle(r)
	if err != nil || foster.Groups[0].Spawns[0].Direction != 4 || r.FosterPlan != nil {
		t.Fatal("stored Foster plan not prepared independently", err)
	}
	h.Config.Settlement = persistence.RewardRules{}
	if _, err = h.prepareFosterBattle(r); err == nil {
		t.Fatal("Foster preparation accepted missing rewards")
	}
	h.Config.Settlement = rewards
	exec("UPDATE stage_access SET rules=? WHERE id=1", accessData)
	profile := make([]byte, protocol.RoleProfileSize)
	protocol.WriteUint16(profile, persistence.LevelOffset, 1)
	exec("INSERT INTO counters VALUES('battle',1)")
	exec("INSERT INTO accounts VALUES(?,?,10,3),(?,?,10,3)", owner.UID, profile, peer.UID, []byte{1})
	p := settlementReport(r)
	for _, m := range r.Members {
		protocol.WriteUint16(p, int(m.Slot)*87+65, 1)
	}
	// A corrupt later account rolls back the whole room and schedules a retry.
	if err = h.route(owner, owner.game(), protocol.Message{ID: 4110, Payload: p}); err != nil {
		t.Fatal(err)
	}
	if r.LoadTimer == nil || r.Stage != "finishing" {
		t.Fatal("failure lost pending state")
	}
	r.LoadTimer.Stop()
	defer func() {
		if r.LoadTimer != nil {
			r.LoadTimer.Stop()
		}
	}()
	roomOutputs(t, owner, 20150)
	roomOutputs(t, peer)
	var gold int
	if err = db.QueryRow("SELECT gold FROM accounts WHERE uid=?", owner.UID).Scan(&gold); err != nil || gold != 10 {
		t.Fatal("partial reward", gold, err)
	}
	exec("UPDATE accounts SET profile=? WHERE uid=?", profile, peer.UID)
	// Same native report can resume after the transient persistence failure.
	if err = h.route(owner, owner.game(), protocol.Message{ID: 4110, Payload: p}); err != nil {
		t.Fatal(err)
	}
	if r.Stage != "settlement" || r.LoadTimer != nil {
		t.Fatal("settlement did not finish")
	}
	for _, s := range []*Session{owner, peer} {
		out := roomOutputs(t, s, 4300, 1240, 1230, 4120)
		if protocol.ReadUint32(out[1].Payload, 0) != 17 || protocol.ReadUint32(out[2].Payload, 0) != 5 {
			t.Fatal("wrong balances")
		}
		rows, err := protocol.ParseStageResults(out[3].Payload)
		if err != nil || len(rows) != 2 || rows[1].UID != s.UID {
			t.Fatal("recipient profile not last", err)
		}
		for _, row := range rows {
			if row.Waves != 1 || row.ElapsedSeconds < 125 || row.Experience != 20 || row.Gold != 7 || protocol.ReadUint32(row.Raw[:], 87) != 0xffffffff {
				t.Fatal("bad result record", row)
			}
		}
		if s.game().Phase != "settlement" {
			t.Fatal("wrong client phase")
		}
	}
	roomOutputs(t, outsider)
	if err = h.route(owner, owner.game(), protocol.Message{ID: 4110, Payload: p}); err != nil {
		t.Fatal(err)
	}
	roomOutputs(t, owner)
	roomOutputs(t, peer)
	var receipts int
	if err = db.QueryRow("SELECT COUNT(*) FROM battle_settlements").Scan(&receipts); err != nil || receipts != 1 {
		t.Fatal("duplicate receipt", receipts, err)
	}
	// Follow the persisted multiplayer result through both native acknowledgements.
	if err = h.route(owner, owner.game(), protocol.Message{ID: 4115, Payload: make([]byte, 4)}); err != nil {
		t.Fatal(err)
	}
	if r.Stage != "settlement" {
		t.Fatal("one acknowledgement prematurely completed result screen")
	}
	if err = h.route(peer, peer.game(), protocol.Message{ID: 4115, Payload: make([]byte, 4)}); err != nil {
		t.Fatal(err)
	}
	if r.Stage != "room" || owner.game().Phase != "room" || peer.game().Phase != "room" {
		t.Fatal("both acknowledgements failed to return room")
	}
	for _, s := range []*Session{owner, peer} {
		roomOutputs(t, s, protocol.MsgPlayerNotReady, protocol.MsgPlayerNotReady)
	}
	// A verified all-zero-health reason 2 uses failure configuration, not PvP.
	r.Serial = 2
	r.Stage = "battle"
	r.Reports = nil
	r.StageWaves.finished = false
	r.StageWaves.index = 0
	h.Config.Settlement.StageRewards[0].Failed = persistence.StageReward{RewardBundle: persistence.RewardBundle{Gold: 1}}
	exec("UPDATE counters SET value=2 WHERE name='battle'")
	p = settlementReport(r)
	for _, m := range r.Members {
		m.Session.game().Phase = "battle"
		protocol.WriteUint16(p, int(m.Slot)*87+65, 2)
		protocol.WriteUint16(p, int(m.Slot)*87+2, 0)
	}
	if err = h.route(owner, owner.game(), protocol.Message{ID: 4110, Payload: p}); err != nil {
		t.Fatal(err)
	}
	for _, s := range []*Session{owner, peer} {
		out := roomOutputs(t, s, 4300, 1240, 1230, 4120)
		rows, err := protocol.ParseStageResults(out[3].Payload)
		if err != nil || rows[0].ResultValue != 2 || rows[0].Gold != 1 || rows[0].Experience != 0 || rows[0].Waves != 0 || protocol.ReadUint32(out[1].Payload, 0) != 18 {
			t.Fatal("failure rewards mixed with clearance", err)
		}
	}
	// Mode 10 uses the same atomic stage rewards with the common result page.
	for _, scenario := range []struct {
		serial uint32
		reason uint16
		gold   uint32
	}{{3, 1, 25}, {4, 2, 26}} {
		r.Request[46] = byte(protocol.FosterMode)
		r.Serial, r.Stage, r.Reports, r.StageWaves = scenario.serial, "battle", nil, nil
		r.FosterPlan = foster
		r.FosterSpawned, r.FosterRetired = []int{1}, []int{1}
		r.FosterFinishReported = scenario.reason == 1
		exec("UPDATE counters SET value=? WHERE name='battle'", scenario.serial)
		p = settlementReport(r)
		for _, m := range r.Members {
			m.Session.game().Phase = "battle"
			protocol.WriteUint16(p, int(m.Slot)*87+65, scenario.reason)
			if scenario.reason == 2 {
				protocol.WriteUint16(p, int(m.Slot)*87+2, 0)
			}
		}
		if err = h.route(owner, owner.game(), protocol.Message{ID: 4110, Payload: p}); err != nil {
			t.Fatal(err)
		}
		if r.Stage != "settlement" {
			t.Fatal("Foster report did not settle")
		}
		for _, s := range []*Session{owner, peer} {
			out := roomOutputs(t, s, 4300, 1240, 1230, 4120)
			if protocol.ReadUint32(out[1].Payload, 0) != scenario.gold {
				t.Fatal("Foster balance mismatch")
			}
			rows, e := protocol.ParseStageResults(out[3].Payload)
			if e != nil || len(rows) != 2 || rows[1].UID != s.UID {
				t.Fatal("Foster profile order", e)
			}
			for _, row := range rows {
				if row.ResultValue != byte(scenario.reason) || row.Waves != 0 || row.ElapsedSeconds != 0 || row.GradeValue != 0 {
					t.Fatal("wrong Foster result schema")
				}
			}
		}
		if err = h.route(owner, owner.game(), protocol.Message{ID: 4110, Payload: p}); err != nil {
			t.Fatal(err)
		}
		roomOutputs(t, owner)
		roomOutputs(t, peer)
		if err = db.QueryRow("SELECT COUNT(*) FROM battle_settlements").Scan(&receipts); err != nil || receipts != int(scenario.serial) {
			t.Fatal("Foster duplicate reward", receipts, err)
		}
	}

	// One native finish report arrives before the final UDP progress. No second
	// report is sent: the retained report must settle both players exactly once.
	for i, mode := range []protocol.RoomType{protocol.FosterMode, protocol.StageAssault} {
		h.lockState()
		r.Serial, r.Stage, r.Reports = uint32(5+i), "battle", nil
		r.Request[46] = byte(mode)
		r.FosterPlan, r.FosterFinishReported = foster, false
		r.StageWaves, _ = newStageWaves([]StageWavePlan{{Monsters: map[uint32]uint32{7: 1}}})
		p = settlementReport(r)
		for _, member := range r.Members {
			member.Session.game().Phase = "battle"
			protocol.WriteUint16(p, int(member.Slot)*87+65, 1)
		}
		h.unlockState()
		exec("UPDATE counters SET value=? WHERE name='battle'", r.Serial)
		frame := tunnel.Frame{Op: "data", Channel: 1, Data: mustEncodedMessage(t, protocol.Message{ID: 4110, Payload: p})}
		if err := h.Handle(owner, frame); err != nil {
			t.Fatal(err)
		}
		h.lockState()
		pending := r.pendingStageFinish != nil && r.Stage == "battle"
		if pending {
			r.pendingStageFinish.deadline = time.Now().Add(-time.Second)
		}
		h.unlockState()
		// Simulate the former five-second cutoff before progress catches up.
		warningDeadline := time.Now().Add(2 * time.Second)
		for pending {
			h.Mutex.RLock()
			warned := r.pendingStageFinish != nil && r.pendingStageFinish.warned
			h.Mutex.RUnlock()
			if warned {
				break
			}
			if time.Now().After(warningDeadline) {
				t.Fatal("finish warning not emitted")
			}
			time.Sleep(10 * time.Millisecond)
		}
		if pending {
			roomOutputs(t, owner, 20150)
			roomOutputs(t, peer, 20150)
		}
		h.lockState()
		r.FosterFinishReported = true
		r.StageWaves.finished, r.StageWaves.index = true, 1
		h.unlockState()
		if !pending {
			t.Fatal("early finish was discarded")
		}
		deadline := time.Now().Add(2 * time.Second)
		for {
			h.Mutex.RLock()
			settled := r.Stage == "settlement"
			h.Mutex.RUnlock()
			if settled {
				break
			}
			if time.Now().After(deadline) {
				t.Fatal("late progress did not resume settlement")
			}
			time.Sleep(10 * time.Millisecond)
		}
		for _, member := range []*Session{owner, peer} {
			roomOutputs(t, member, 4300, 1240, 1230, 4120)
		}
		if err := h.Handle(owner, frame); err != nil {
			t.Fatal(err)
		}
		roomOutputs(t, owner)
		roomOutputs(t, peer)
		if err := db.QueryRow("SELECT COUNT(*) FROM battle_settlements WHERE serial=?", r.Serial).Scan(&receipts); err != nil || receipts != 1 {
			t.Fatal("duplicate deferred payout", receipts, err)
		}
	}
}
