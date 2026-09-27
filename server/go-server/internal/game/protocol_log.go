package game

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"kungfu.local/server/internal/persistence"
	"kungfu.local/server/internal/protocol"
	"kungfu.local/server/internal/tunnel"
)

// Trace is opt-in. Each emitted line is a complete decoded packet. The bounded
// diagnostic writer may drop lines under congestion and reports that count.
// Queued output is distinguished from a successful socket write.
func (s *Session) tracePacket(direction string, channel uint32, transport string, opcode uint32, payload []byte, redact bool) {
	if s.Trace == nil {
		return
	}
	entry := map[string]any{"time": time.Now().Format(time.RFC3339Nano), "direction": direction, "account": s.Account, "player": s.Nickname, "uid": s.UID, "channel": channel, "transport": transport, "protocol": opcode, "length": len(payload)}
	if c := s.Channels[channel]; c != nil {
		entry["phase"] = c.Phase
	}
	if s.Room != nil {
		entry["room"] = s.Room.ID
	}
	if redact {
		entry["content"] = "[authentication credentials redacted]"
	} else {
		entry["hex"] = hex.EncodeToString(payload)
		if transport == "game" {
			if opcode == 4120 && strings.HasPrefix(direction, "S->C") && s.Room != nil && s.Room.Type() == protocol.StageAssault {
				if rows, err := protocol.ParseStageResults(payload); err == nil {
					var detail strings.Builder
					detail.WriteString("PVE结算界面数据（非开战初始化）")
					for _, row := range rows {
						fmt.Fprintf(&detail, "；UID=%d 结果原值=%d 波数=%d 用时秒=%d 评级=%s（原值%d） 经验=%d 金币=%d 展示物品=%d", row.UID, row.ResultValue, row.Waves, row.ElapsedSeconds, protocol.StageGrade(row.GradeValue), row.GradeValue, row.Experience, row.Gold, row.ItemID)
					}
					entry["content"] = detail.String()
				}
			}
			if opcode == protocol.MsgStageWaveReport && strings.HasPrefix(direction, "C->S") {
				if r, err := protocol.ParseStageWaveReport(payload); err == nil {
					entry["content"] = fmt.Sprintf("客户端波次结束上报：波次=%d，上下文原值=%d，报告字段=%d（已确认发送器写1）；不是通关或发奖凭据", r.Wave, r.ContextValue, r.ReportValue)
				}
			}
			if opcode == protocol.MsgBattlePose && strings.HasPrefix(direction, "S->C") {
				if r, err := protocol.ParseBattlePose(payload); err == nil {
					entry["content"] = fmt.Sprintf("战斗姿态通知：UID=%d，姿态原值=%d；不是席位锁定", r.UID, r.Pose)
				}
			}
			if opcode == 4110 && strings.HasPrefix(direction, "C->S") {
				if rows, err := protocol.ParseBattleReport(payload); err == nil {
					var detail strings.Builder
					detail.WriteString("客户端结算报告（仍需服务器验证）")
					if s.Room != nil && (s.Room.Type() == protocol.StageAssault || s.Room.Type() == protocol.FosterMode) {
						if _, reason, err := protocol.ParsePVEFinishReport(s.Room.Type(), payload); err == nil {
							fmt.Fprintf(&detail, "；关卡原因=%s（%d），不是竞技胜负或发奖凭据", reason.Description(s.Room.Type()), reason)
						} else {
							detail.WriteString("；关卡报告字段不一致或原因未确认")
						}
					}
					for slot, r := range rows {
						if r.UID != 0 {
							fmt.Fprintf(&detail, "；槽位%d UID=%d 血量=%d 结束原因码=%d 房间=%d 场次=%d", slot, r.UID, r.Health, r.FinishCode, r.RoomID, r.Serial)
						}
					}
					entry["content"] = detail.String()
				}
			}
			if opcode == protocol.MsgCreateRoom && strings.HasPrefix(direction, "C->S") && len(payload) == protocol.RoomRequestSize {
				mode := protocol.RoomTypeFromRequest(payload)
				entry["content"] = fmt.Sprintf("创建房间：类型=%s（%d），地图=%d，容量=%d；请求不代表已获准", mode, byte(mode), protocol.ReadUint32(payload, protocol.RoomMapOffset), payload[protocol.RoomCapacityOffset])
			}
			if opcode == protocol.MsgStageWaveControl && strings.HasPrefix(direction, "S->C") {
				if r, err := protocol.ParseStageWaveControl(payload); err == nil {
					entry["content"] = fmt.Sprintf("闯关波次控制：波次=%d，-1触发客户端结束流程；不等于通关奖励凭据", r.Wave)
				}
			}
			if opcode == protocol.MsgBattleLoading && strings.HasPrefix(direction, "S->C") {
				if r, err := protocol.ParseBattleStart(payload); err == nil {
					entry["content"] = fmt.Sprintf("加载战斗：房间=%d，主控槽位=%d，槽位0–7延迟(ms)=%v；0表示未测量，耗时包含客户端处理和服务器调度", r.RoomID, r.ControllerSlot, r.NetworkDelay)
				}
			}
			if opcode == protocol.MsgNetworkDelayProbe && strings.HasPrefix(direction, "S->C") && len(payload) == 0 {
				entry["content"] = "开战前网络检测：等待本玩家返回4140空包"
			}
			if opcode == protocol.MsgNetworkDelayReply && strings.HasPrefix(direction, "C->S") && len(payload) == 0 {
				entry["content"] = "玩家返回网络检测空回执；由服务器核对当前检测状态，不代表检测已通过"
			}
			if opcode == protocol.MsgBattleInputReady && strings.HasPrefix(direction, "C->S") {
				if r, err := protocol.ParseBattleInputReady(payload); err == nil {
					entry["content"] = fmt.Sprintf("输入就绪请求：房间=%d，申报UID=%d，客户端尾值=%d（语义未确认，不作为授权）", r.RoomID, r.UID, r.ClientValue)
				}
			}
			if (opcode == 3090 || opcode == 3105) && strings.HasPrefix(direction, "S->C") {
				if rows, err := protocol.ParseRoomMembers(payload); err == nil && (opcode != 3090 || len(rows) == 1) {
					var detail strings.Builder
					fmt.Fprintf(&detail, "房间成员：%d条", len(rows))
					for _, r := range rows {
						kind := "参战"
						if r.Spectator() {
							kind = "观战"
						}
						fmt.Fprintf(&detail, "；UID=%d %s 槽位=%d 装备=%d", r.UID(), kind, r.Slot(), r.EquipmentCount())
					}
					entry["content"] = detail.String()
				}
			}
			if opcode == protocol.MsgPlayerLeftRoom && len(payload) == 8 && strings.HasPrefix(direction, "S->C") {
				entry["content"] = fmt.Sprintf("玩家离开房间：UID=%d", protocol.ReadUint64(payload, 0))
			}
			if opcode == protocol.MsgJoinRoom && strings.HasPrefix(direction, "C->S") {
				if r, err := protocol.ParseRoomJoinRequest(payload); err == nil {
					mode := fmt.Sprintf("未确认模式%d", r.Mode)
					if r.Mode == protocol.JoinAsPlayer {
						mode = "参战"
					}
					if r.Mode == protocol.JoinAsSpectator {
						mode = "观战（尚未接入完整业务）"
					}
					entry["content"] = fmt.Sprintf("请求进入房间=%d，方式=%s", r.RoomID, mode)
				}
			}
			if opcode == protocol.MsgWatchGameRequest && strings.HasPrefix(direction, "C->S") {
				entry["content"] = "观战请求（A_WATCH_GAME_REQ）；字段尚未确认，请查看原始数据"
			}
			if opcode == protocol.MsgWatchGameAck && strings.HasPrefix(direction, "S->C") {
				entry["content"] = "观战应答（A_WATCH_GAME_ACK）；结果字段尚未确认，不能据此判断成功"
			}
			if opcode == protocol.MsgRenewItemResult && strings.HasPrefix(direction, "S->C") {
				if r, err := protocol.ParseRenewalResult(payload); err == nil {
					if r.Succeeded {
						entry["content"] = fmt.Sprintf("续费成功回执：库存实例=%d", r.InventoryInstance)
					} else {
						entry["content"] = "续费失败回执"
					}
				}
			}
			if opcode == protocol.MsgRenewItem && strings.HasPrefix(direction, "C->S") {
				if r, err := protocol.ParseRenewalRequest(payload); err == nil {
					entry["content"] = fmt.Sprintf("请求续费：库存实例=%d，商品目录键=%d，申报金额=%d（不代表已扣款），操作原值=%d，发起UID=%d，目标UID=%d", r.InventoryInstance(), r.CatalogKey(), r.QuotedAmount(), r.Operation(), r.SenderUID(), r.RecipientUID())
				}
			}
			if opcode == protocol.MsgKickRoomPlayer {
				if r, err := protocol.ParseRoomKickRequest(payload); err == nil {
					entry["content"] = fmt.Sprintf("请求踢出玩家 UID=%d；客户端标志=%d（不代表房主权限）", r.TargetUID, r.ClientFlag)
				}
			}
			// Our own notice format includes the terminating NUL in its length.
			if opcode == 20150 && strings.HasPrefix(direction, "S->C") && len(payload) == 215 {
				n := int(payload[12])
				if n > 0 && n <= 200 && payload[12+n] == 0 {
					if text, err := persistence.DecodeGBK(payload[13 : 12+n]); err == nil {
						entry["content"] = text
					}
				}
			}
		}
		if text, err := persistence.DecodeGBK(payload); err == nil {
			entry["text_gbk"] = text
		}
		if opcode == 8071 && len(payload) >= 4 {
			entry["subprotocol"] = protocol.ReadUint32(payload, 0)
			if transport == "game" {
				if sender, rows, err := protocol.ParseFosterPositions(payload); err == nil {
					entry["content"] = fmt.Sprintf("模式10初始位置：申报UID=%d，六条记录=%v；加载期位置表，不是普通移动", sender, rows)
				}
				if r, err := protocol.ParsePVEBlockCreate(payload); err == nil {
					entry["content"] = fmt.Sprintf("PVE创建地图阻挡：申报UID=%d，阻挡ID=%d，布尔原值=%t，四角=%v", r.Sender, r.ID, r.Flag, r.Corners)
				}
				if r, err := protocol.ParsePVEBlockRemove(payload); err == nil {
					entry["content"] = fmt.Sprintf("PVE解除地图阻挡：申报UID=%d，阻挡ID=%d；不是通关或发奖凭据", r.Sender, r.ID)
				}
				if r, err := protocol.ParsePVEActorCreate(payload); err == nil {
					entry["content"] = fmt.Sprintf("PVE创建怪物：申报UID=%d，实体=%d，模板原值=%d，位置=%v，朝向原值=%d；仍需服务端授权", r.Sender, r.Actor, r.TemplateValue, r.Position, r.DirectionValue)
				}
				if r, err := protocol.ParseBattleHealth(payload); err == nil {
					entry["content"] = fmt.Sprintf("伤害/治疗8121：申报UID=%d，目标=%d，来源=%d，伤害量=%g（负值为治疗），目标MP参数=%g，来源MP参数=%g（正伤害分支使用），本局标识=%d；此包不包含已确认的最终HP字段", r.Sender, r.Target, r.Source, r.Damage, r.TargetManaDelta, r.SourceManaDelta, r.Context)
				}
				if r, err := protocol.ParsePVEActorRemove(payload); err == nil {
					entry["content"] = fmt.Sprintf("PVE移除怪物：申报UID=%d，实体=%d；不是击杀或通关凭据", r.Sender, r.Actor)
				}
				if r, err := protocol.ParseStageWaveEnd(payload); err == nil {
					entry["content"] = fmt.Sprintf("PVE结束标记20407：申报UID=%d，本局标识=%d；模式10仅在房主和本局校验后同步，不作为发奖授权", r.Sender, r.ContextValue)
				}
			}
		}
	}
	var encoded bytes.Buffer
	encoder := json.NewEncoder(&encoded)
	encoder.SetEscapeHTML(false)
	if encoder.Encode(entry) == nil {
		s.Trace.Print(string(bytes.TrimSuffix(encoded.Bytes(), []byte("\n"))))
	}
}

func (s *Session) traceFrame(direction string, frame tunnel.Frame) {
	if s.Trace == nil {
		return
	}
	switch frame.Op {
	case "data":
		c := s.Channels[frame.Channel]
		if c != nil && c.Kind == "sdk" {
			flags, body, err := protocol.ReadLogin(bytes.NewReader(frame.Data))
			if err == nil {
				id := uint32(protocol.ReadUint16(body, 0))
				s.tracePacket(direction, frame.Channel, "sdk", id, body[2:], flags == 1 || id == protocol.MsgSDKLoginResponse)
			}
			return
		}
		decoder := protocol.Decoder{}
		messages, err := decoder.Feed(frame.Data)
		if err != nil {
			s.tracePacket(direction, frame.Channel, "invalid-game-frame", 0, frame.Data, false)
			return
		}
		for _, m := range messages {
			s.tracePacket(direction, frame.Channel, "game", m.ID, m.Payload, false)
		}
	case "udp":
		var id uint32
		if len(frame.Data) >= 4 {
			id = uint32(protocol.ReadUint16(frame.Data, 2))
		}
		s.tracePacket(direction, frame.Channel, "udp", id, frame.Data, false)
	default:
		// Do not marshal arbitrary tunnel envelopes: they may contain credentials.
		payload, _ := json.Marshal(map[string]any{"op": frame.Op, "kind": frame.Kind, "port": frame.Port, "value": frame.Value, "uid": frame.UID, "error": frame.Error})
		s.tracePacket(direction, frame.Channel, "tunnel:"+frame.Op, 0, payload, false)
	}
}
