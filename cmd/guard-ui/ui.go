//go:build windows

package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/lxn/walk"
	. "github.com/lxn/walk/declarative"

	"openclaw-guard-kit/internal/protocol"
	"openclaw-guard-kit/notify"
)

type UIApp struct {
	cfg    UIConfig
	client *GuardCLI

	mw            *walk.MainWindow
	tray          *walk.NotifyIcon
	icon          *walk.Icon
	detectorVal   *walk.Label
	guardVal      *walk.Label
	gatewayVal    *walk.Label
	agentVal      *walk.Label
	configVal     *walk.Label
	monitoringVal *walk.Label

	// Telegram tab widgets
	telegramTokenEdit   *walk.LineEdit
	telegramCodeEdit    *walk.LineEdit
	telegramStatusLabel *walk.Label
	telegramBoundLabel  *walk.Label
	telegramTestLabel   *walk.Label

	// Telegram 配对状态
	telegramPairingWatcher *notify.TelegramPairingWatcher
	telegramPairingCode    string
	telegramVerifiedBotID  string
	telegramVerifiedToken  string // 保存 token 用于发送测试消息
	telegramPairingResult  *notify.PairingResult

	// Feishu tab widgets
	feishuAppIDEdit     *walk.LineEdit
	feishuAppSecretEdit *walk.LineEdit
	feishuCodeEdit      *walk.LineEdit
	feishuStatusLabel   *walk.Label
	feishuBoundLabel    *walk.Label
	feishuTestLabel     *walk.Label
	// Feishu 配对状态
	feishuPairingWatcher *notify.FeishuPairingWatcher
	feishuPairingCode    string
	feishuVerifiedAppID  string
	feishuPairingResult  *notify.PairingResult
	// WeCom 配对状态
	wecomPairingWatcher *notify.WecomPairingWatcher
	wecomPairingCode    string
	wecomVerifiedBotID  string
	wecomPairingResult  *notify.PairingResult

	wecomBotIDEdit   *walk.LineEdit
	wecomSecretEdit  *walk.LineEdit
	wecomCodeEdit    *walk.LineEdit
	wecomStatusLabel *walk.Label
	wecomBoundLabel  *walk.Label
	wecomTestLabel   *walk.Label

	resultStatus *walk.Label
	resultDetail *walk.Label
	resultHint   *walk.Label

	pauseMonitorAction  *walk.Action
	resumeMonitorAction *walk.Action
	openLogsAction      *walk.Action

	trayNotifyPrimed    bool
	lastDetectorTrayKey string
	lastGuardTrayKey    string
}

func Run(cfg UIConfig) error {
	ui := &UIApp{
		cfg:    cfg,
		client: NewGuardCLI(cfg),
	}

	if err := ui.createMainWindow(); err != nil {
		return err
	}
	defer ui.dispose()

	if err := ui.setupTray(); err != nil {
		return err
	}

	ui.mw.SetSuspended(true)

	ui.setupCloseBehavior()
	ui.reloadStaticViews()
	ui.refreshStatusAsync("界面已启动", "托盘程序已运行。", "现在可以查看状态和配置机器人。")
	ui.startPolling()

	if cfg.StartHidden {
		ui.mw.Hide()
		_ = ui.tray.ShowInfo("OpenClaw Guard", "已最小化到托盘，点击右下角图标可打开面板。")
	} else {
		ui.mw.Show()
		ui.mw.SetVisible(true)
		_ = ui.mw.Activate()
	}

	ui.mw.SetSuspended(false)

	ui.mw.Run()
	return nil
}

func (ui *UIApp) createMainWindow() error {
	return MainWindow{
		AssignTo: &ui.mw,
		Title:    "OpenClaw Guard 控制面板",
		MinSize:  Size{Width: 520, Height: 480},
		Size:     Size{Width: 560, Height: 520},
		Visible:  true,
		Layout:   VBox{},
		Children: []Widget{
			// 顶部状态卡（精简版 + 刷新按钮）
			GroupBox{
				Title:  "运行状态",
				Layout: Grid{Columns: 3},
				Children: []Widget{
					Label{Text: "监控状态"},
					Label{AssignTo: &ui.monitoringVal, Text: "未知"},
					Label{Text: ""},

					Label{Text: "Detector 状态"},
					Label{AssignTo: &ui.detectorVal, Text: "未知"},
					Label{Text: ""}, // empty

					Label{Text: "守护状态"},
					Label{AssignTo: &ui.guardVal, Text: "未知"},
					Label{Text: ""}, // empty

					Label{Text: "网关状态"},
					Label{AssignTo: &ui.gatewayVal, Text: "未知"},
					Label{Text: ""}, // empty

					Label{Text: "当前 Agent"},
					Label{AssignTo: &ui.agentVal, Text: ui.cfg.AgentID},
					Label{Text: ""}, // empty

					Label{Text: "配置路径"},
					Label{AssignTo: &ui.configVal, Text: ui.cfg.ConfigPath},
					PushButton{Text: "刷新状态", OnClicked: func() {
						ui.refreshStatusAsync("处理中", "正在刷新状态...", "")
					}, MinSize: Size{Width: 80, Height: 0}},
				},
			},

			// TabWidget: Telegram / 飞书 / 企业微信 / 维护
			Composite{
				StretchFactor: 1,
				Layout:        VBox{},
				Children: []Widget{
					TabWidget{
						StretchFactor: 1,
						Pages: []TabPage{
							// ===== Telegram Tab =====
							{
								Title:  "Telegram",
								Layout: VBox{},
								Children: []Widget{
									// 1. 凭证配置区
									GroupBox{
										Title:  "Telegram 凭证配置",
										Layout: Grid{Columns: 2},
										Children: []Widget{
											Label{Text: "Bot Token:"},
											LineEdit{AssignTo: &ui.telegramTokenEdit},
											Label{Text: "(请输入 Telegram Bot Token)", TextColor: 0x808080},
										},
									},
									// 2. 步骤说明区
									GroupBox{
										Title:  "配置步骤（软件通知绑定）",
										Layout: VBox{},
										Children: []Widget{
											Label{
												Text: "第1步: 填写上方 Bot Token\n" +
													"第2步: 点击「测试连接」按钮\n" +
													"第3步: 复制下方完整命令 /pair <配对码>\n" +
													"       （如: /pair ABC123）并发送给机器人\n" +
													"第4步: 面板收到后会自动完成绑定\n" +
													"第5步: 点击「发送测试消息」确认成功",
												TextColor: 0x006666,
											},
										},
									},
									// 3. 配对输入区
									GroupBox{
										Title:  "配对绑定",
										Layout: Grid{Columns: 3},
										Children: []Widget{
											Label{Text: "配对码:"},
											LineEdit{AssignTo: &ui.telegramCodeEdit, ReadOnly: true},
											Label{Text: ""}, // empty

											Label{Text: ""},
											PushButton{Text: "自动绑定说明", OnClicked: ui.handleTelegramBinding},
											Label{Text: ""},
										},
									},
									// 4. 当前绑定状态卡
									GroupBox{
										Title:  "当前绑定状态",
										Layout: Grid{Columns: 2},
										Children: []Widget{
											Label{Text: "当前状态:"},
											Label{AssignTo: &ui.telegramStatusLabel, Text: "未绑定"},

											Label{Text: "绑定用户:"},
											Label{AssignTo: &ui.telegramBoundLabel, Text: "-"},

											Label{Text: "上次测试结果:"},
											Label{AssignTo: &ui.telegramTestLabel, Text: "-"},
										},
									},
									// 主按钮区
									Composite{
										Layout: HBox{},
										Children: []Widget{
											PushButton{Text: "测试连接", OnClicked: ui.handleTelegramTest},
											PushButton{Text: "发送测试消息", OnClicked: ui.handleTelegramTestMsg},
											PushButton{Text: "解除绑定", OnClicked: ui.handleTelegramUnbind},
											HSpacer{},
										},
									},
								},
							},
							// ===== 飞书 Tab =====
							{
								Title:  "飞书",
								Layout: VBox{},
								Children: []Widget{
									// 1. 凭证配置区
									GroupBox{
										Title:  "飞书凭证配置",
										Layout: Grid{Columns: 2},
										Children: []Widget{
											Label{Text: "App ID:"},
											LineEdit{AssignTo: &ui.feishuAppIDEdit},
											Label{Text: "(请输入飞书 App ID)", TextColor: 0x808080},
											Label{Text: "App Secret:"},
											LineEdit{AssignTo: &ui.feishuAppSecretEdit, PasswordMode: true},
											Label{Text: "(请输入飞书 App Secret)", TextColor: 0x808080},
										},
									},
									// 2. 步骤说明区
									GroupBox{
										Title:  "配置步骤",
										Layout: VBox{},
										Children: []Widget{
											Label{
												Text: "第1步: 填写上方 App ID 和 App Secret\n" +
													"第2步: 点击「测试连接」按钮\n" +
													"第3步: 复制下方完整命令 /pair <配对码> 并发送给飞书机器人\n" +
													"第4步: 面板收到后会自动完成绑定\n" +
													"第5步: 点击「发送测试消息」确认成功",
												TextColor: 0x006666,
											},
										},
									},
									// 3. 配对输入区
									GroupBox{
										Title:  "配对绑定",
										Layout: Grid{Columns: 3},
										Children: []Widget{
											Label{Text: "配对码:"},
											LineEdit{AssignTo: &ui.feishuCodeEdit, ReadOnly: true},
											PushButton{Text: "自动绑定说明", OnClicked: ui.handleFeishuBinding},
										},
									},
									// 4. 当前绑定状态卡
									GroupBox{
										Title:  "当前绑定状态",
										Layout: Grid{Columns: 2},
										Children: []Widget{
											Label{Text: "当前状态:"},
											Label{AssignTo: &ui.feishuStatusLabel, Text: "未配置"},

											Label{Text: "绑定用户:"},
											Label{AssignTo: &ui.feishuBoundLabel, Text: "-"},

											Label{Text: "上次测试结果:"},
											Label{AssignTo: &ui.feishuTestLabel, Text: "-"},
										},
									},
									// 主按钮区
									Composite{
										Layout: HBox{},
										Children: []Widget{
											PushButton{Text: "测试连接", OnClicked: ui.handleFeishuTest},
											PushButton{Text: "发送测试消息", OnClicked: ui.handleFeishuTestMsg},
											PushButton{Text: "解除绑定", OnClicked: ui.handleFeishuUnbind},
											HSpacer{},
										},
									},
								},
							},
							// ===== 企业微信 Tab =====
							{
								Title:  "企业微信",
								Layout: VBox{},
								Children: []Widget{
									// 1. 凭证配置区
									GroupBox{
										Title:  "企业微信凭证配置",
										Layout: Grid{Columns: 2},
										Children: []Widget{
											Label{Text: "Bot ID:"},
											LineEdit{AssignTo: &ui.wecomBotIDEdit},
											Label{Text: "(请输入企业微信 Bot ID)", TextColor: 0x808080},

											Label{Text: "Secret:"},
											LineEdit{AssignTo: &ui.wecomSecretEdit, PasswordMode: true},
											Label{Text: "(请输入企业微信 Bot Secret)", TextColor: 0x808080},
										},
									},
									// 2. 步骤说明区
									GroupBox{
										Title:  "配置步骤",
										Layout: VBox{},
										Children: []Widget{
											Label{
												Text: "第1步: 填写上方 Bot ID 和 Secret\n" +
													"第2步: 点击「测试连接」按钮\n" +
													"第3步: 复制下方完整命令 /pair <配对码> 并发送给企业微信机器人\n" +
													"第4步: 面板收到后会自动完成绑定\n" +
													"第5步: 点击「发送测试消息」确认成功",
												TextColor: 0x006666,
											},
										},
									},
									// 3. 配对输入区
									GroupBox{
										Title:  "配对绑定",
										Layout: Grid{Columns: 3},
										Children: []Widget{
											Label{Text: "配对码:"},
											LineEdit{AssignTo: &ui.wecomCodeEdit, ReadOnly: true},
											Label{Text: ""}, // empty

											PushButton{Text: "自动绑定说明", OnClicked: ui.handleWecomBinding},
										},
									},
									// 4. 当前绑定状态卡
									GroupBox{
										Title:  "当前绑定状态",
										Layout: Grid{Columns: 2},
										Children: []Widget{
											Label{Text: "当前状态:"},
											Label{AssignTo: &ui.wecomStatusLabel, Text: "未配置"},

											Label{Text: "绑定用户:"},
											Label{AssignTo: &ui.wecomBoundLabel, Text: "-"},

											Label{Text: "上次测试结果:"},
											Label{AssignTo: &ui.wecomTestLabel, Text: "-"},
										},
									},
									// 主按钮区
									Composite{
										Layout: HBox{},
										Children: []Widget{
											PushButton{Text: "测试连接", OnClicked: ui.handleWecomTest},
											PushButton{Text: "发送测试消息", OnClicked: ui.handleWecomTestMsg},
											PushButton{Text: "解除绑定", OnClicked: ui.handleWecomUnbind},
											HSpacer{},
										},
									},
								},
							},
							// ===== 维护 Tab =====
							{
								Title:  "维护",
								Layout: VBox{},
								Children: []Widget{
									GroupBox{
										Title:  "常用维护",
										Layout: VBox{},
										Children: []Widget{
											Composite{
												Layout: HBox{},
												Children: []Widget{
													PushButton{
														Text: "刷新状态",
														OnClicked: func() {
															ui.refreshStatusAsync("处理中", "正在刷新状态...", "")
														},
													},
													PushButton{
														Text: "打开维护目录",
														OnClicked: func() {
															ui.openPath(ui.client.OpenConfigDir())
														},
													},
													PushButton{
														Text: "打开日志目录",
														OnClicked: func() {
															ui.openPath(ui.client.OpenLogsDir())
														},
													},
													HSpacer{},
												},
											},
											Label{
												Text:      "用于日常查看和快速定位问题。",
												TextColor: 0x666666,
											},
										},
									},

									GroupBox{
										Title:  "监控控制",
										Layout: VBox{},
										Children: []Widget{
											Composite{
												Layout: HBox{},
												Children: []Widget{
													PushButton{Text: "暂停监控", OnClicked: ui.handlePauseGuard},
													PushButton{Text: "恢复监控", OnClicked: ui.handleResumeGuard},
													HSpacer{},
												},
											},
											Label{
												Text:      "暂停后不会自动回滚配置；恢复后继续检测非法改动。",
												TextColor: 0x666666,
											},
										},
									},

									GroupBox{
										Title:  "守护诊断",
										Layout: VBox{},
										Children: []Widget{
											Composite{
												Layout: HBox{},
												Children: []Widget{
													PushButton{Text: "测试守护写入", OnClicked: ui.handleGuardWriteTest},
													HSpacer{},
												},
											},
											Label{
												Text:      "用于排查守护链路与写入流程。",
												TextColor: 0x666666,
											},
										},
									},
								},
							},
						},
					},
				},
			},

			// 底部操作结果提示栏
			GroupBox{
				Title:   "操作结果",
				MinSize: Size{Width: 0, Height: 80},
				MaxSize: Size{Width: 0, Height: 80},
				Layout:  Grid{Columns: 2},
				Children: []Widget{
					Label{Text: "当前状态"},
					Label{AssignTo: &ui.resultStatus, Text: "未执行"},
					Label{Text: "结果说明"},
					Label{AssignTo: &ui.resultDetail, Text: "等待操作..."},
					Label{Text: "下一步建议"},
					Label{AssignTo: &ui.resultHint, Text: "可以先从托盘菜单打开面板并刷新状态。"},
				},
			},
		},
	}.Create()
}

func (ui *UIApp) setupTray() error {
	tray, err := walk.NewNotifyIcon(ui.mw)
	if err != nil {
		return err
	}
	ui.tray = tray

	icon, err := walk.NewIconFromSysDLL("shell32", 44)
	if err == nil {
		ui.icon = icon
		_ = ui.mw.SetIcon(icon)
		_ = ui.tray.SetIcon(icon)
	}

	_ = ui.tray.SetToolTip("OpenClaw Guard 控制面板")
	if err := ui.tray.SetVisible(true); err != nil {
		return err
	}

	openAction := walk.NewAction()
	_ = openAction.SetText("打开面板")
	openAction.Triggered().Attach(func() { ui.showPanel() })
	_ = ui.tray.ContextMenu().Actions().Add(openAction)

	pauseAction := walk.NewAction()
	ui.pauseMonitorAction = pauseAction
	_ = pauseAction.SetText("暂停监控")
	pauseAction.Triggered().Attach(func() { ui.handlePauseGuard() })
	_ = ui.tray.ContextMenu().Actions().Add(pauseAction)

	resumeAction := walk.NewAction()
	ui.resumeMonitorAction = resumeAction
	_ = resumeAction.SetText("恢复监控")
	resumeAction.Triggered().Attach(func() { ui.handleResumeGuard() })
	_ = ui.tray.ContextMenu().Actions().Add(resumeAction)

	refreshAction := walk.NewAction()
	_ = refreshAction.SetText("刷新状态")
	refreshAction.Triggered().Attach(func() { ui.refreshStatusAsync("处理中", "正在刷新状态...", "") })
	_ = ui.tray.ContextMenu().Actions().Add(refreshAction)

	openLogsAction := walk.NewAction()
	ui.openLogsAction = openLogsAction
	_ = openLogsAction.SetText("打开日志目录")
	openLogsAction.Triggered().Attach(func() { ui.openPath(ui.client.OpenLogsDir()) })
	_ = ui.tray.ContextMenu().Actions().Add(openLogsAction)

	_ = ui.tray.ContextMenu().Actions().Add(walk.NewSeparatorAction())

	exitAction := walk.NewAction()
	_ = exitAction.SetText("退出")
	exitAction.Triggered().Attach(func() { ui.exit() })
	_ = ui.tray.ContextMenu().Actions().Add(exitAction)

	ui.tray.MouseDown().Attach(func(x, y int, button walk.MouseButton) {
		if button == walk.LeftButton {
			ui.showPanel()
		}
	})

	ui.updateTrayActions(StatusSnapshot{})
	return nil
}

func (ui *UIApp) setupCloseBehavior() {
	ui.mw.Closing().Attach(func(canceled *bool, _ walk.CloseReason) {
		*canceled = true
		ui.mw.Hide()
	})
}

func (ui *UIApp) startPolling() {
	ticker := time.NewTicker(time.Duration(ui.cfg.PollSeconds) * time.Second)
	go func() {
		for range ticker.C {
			if ui.mw == nil {
				return
			}
			ui.refreshStatusSilent()
		}
	}()
}

func (ui *UIApp) refreshStatusSilent() {
	ctx := context.Background()
	snapshot := ui.client.Snapshot(ctx)
	ui.mw.Synchronize(func() {
		ui.applySnapshot(snapshot)
		ui.reloadStaticViews()
	})
}

func (ui *UIApp) refreshStatusAsync(status, detail, hint string) {
	ui.showResult(status, detail, hint)
	go func() {
		ctx := context.Background()
		snapshot := ui.client.Snapshot(ctx)
		ui.mw.Synchronize(func() {
			ui.applySnapshot(snapshot)
			ui.reloadStaticViews()
			ui.showResult("成功", "状态已刷新。", "现在可以继续操作。")
		})
	}()
}

func (ui *UIApp) applySnapshot(snapshot StatusSnapshot) {
	ui.detectorVal.SetText(fallbackText(snapshot.DetectorStatus, "未知"))
	ui.guardVal.SetText(fallbackText(snapshot.GuardStatus, "未知"))
	ui.gatewayVal.SetText(fallbackText(snapshot.GatewayStatus, "未知"))
	ui.monitoringVal.SetText(fallbackText(snapshot.MonitoringStatus, "未知"))
	ui.agentVal.SetText(fallbackText(snapshot.AgentID, ui.cfg.AgentID))
	ui.configVal.SetText(fallbackText(snapshot.ConfigPath, ui.cfg.ConfigPath))

	ui.updateTrayActions(snapshot)
	ui.maybeShowTrayNotifications(snapshot)
}
func (ui *UIApp) updateTrayActions(snapshot StatusSnapshot) {
	paused := snapshot.MonitoringPaused ||
		strings.EqualFold(strings.TrimSpace(snapshot.State), "paused") ||
		strings.EqualFold(strings.TrimSpace(snapshot.GuardStatus), "已暂停")

	if ui.pauseMonitorAction != nil {
		ui.pauseMonitorAction.SetEnabled(!paused)
	}
	if ui.resumeMonitorAction != nil {
		ui.resumeMonitorAction.SetEnabled(paused)
	}
}

func (ui *UIApp) maybeShowTrayNotifications(snapshot StatusSnapshot) {
	if ui.tray == nil {
		return
	}

	detectorKey := ""
	if snapshot.DetectorNotifyType != "" && !snapshot.DetectorNotifyAt.IsZero() {
		detectorKey = snapshot.DetectorNotifyType + "|" + snapshot.DetectorNotifyAt.UTC().Format(time.RFC3339Nano)
	}

	guardKey := strings.TrimSpace(snapshot.LastEvent)

	if !ui.trayNotifyPrimed {
		ui.lastDetectorTrayKey = detectorKey
		ui.lastGuardTrayKey = guardKey
		ui.trayNotifyPrimed = true
		return
	}

	if detectorKey != "" && detectorKey != ui.lastDetectorTrayKey {
		ui.lastDetectorTrayKey = detectorKey

		if snapshot.DetectorNotifyType == protocol.EventGuardAnomaly {
			msg := strings.TrimSpace(snapshot.DetectorNotifyMessage)
			if msg == "" {
				msg = "守护程序异常，请打开控制台或日志查看详情。"
			}
			_ = ui.tray.ShowError("OpenClaw Guard", msg)
		}
	}

	if guardKey != "" && guardKey != ui.lastGuardTrayKey {
		ui.lastGuardTrayKey = guardKey

		switch guardKey {
		case protocol.EventDriftDetected:
			_ = ui.tray.ShowError("OpenClaw Guard", "检测到未授权修改，已触发保护处理。如需手动修改，请右键 Guard 图标并点击“暂停监控”。")
		case protocol.EventRestoreCompleted:
			_ = ui.tray.ShowInfo("OpenClaw Guard", "检测到配置异常变更，已自动恢复到受保护状态。")
		case protocol.EventRestoreFailed:
			_ = ui.tray.ShowError("OpenClaw Guard", "检测到配置异常变更，但自动恢复失败，请尽快检查日志。")
		}
	}
}
func (ui *UIApp) reloadStaticViews() {
	notify.InitCredentialsStore(ui.cfg.RootDir)

	// Telegram 默认态
	ui.telegramStatusLabel.SetText("未绑定")
	ui.telegramBoundLabel.SetText("-")
	ui.telegramTestLabel.SetText("待测试")
	ui.telegramVerifiedBotID = ""

	// Feishu 默认态
	ui.feishuStatusLabel.SetText("未绑定")
	ui.feishuBoundLabel.SetText("-")
	ui.feishuTestLabel.SetText("待测试")
	ui.feishuVerifiedAppID = ""

	// WeCom 默认态
	ui.wecomStatusLabel.SetText("未绑定")
	ui.wecomBoundLabel.SetText("-")
	ui.wecomTestLabel.SetText("待测试")
	ui.wecomVerifiedBotID = ""

	bindingsPath := notify.BindingsPath(ui.cfg.RootDir)
	store, err := notify.NewStore(bindingsPath)
	if err != nil {
		return
	}

	bindings := store.ListBindings()
	for _, b := range bindings {
		switch {
		case b.Channel == "telegram" && b.Status == "bound":
			ui.telegramStatusLabel.SetText("已绑定")
			ui.telegramBoundLabel.SetText(fallbackText(b.DisplayName, b.SenderID))
			if !b.LastTestAt.IsZero() {
				timeStr := b.LastTestAt.Format("15:04")
				if b.ConnectionStatus == "ok" {
					ui.telegramTestLabel.SetText("测试成功 " + timeStr)
				} else if b.ConnectionStatus == "failed" {
					result := b.LastTestResult
					if len(result) > 20 {
						result = result[:20] + "..."
					}
					ui.telegramTestLabel.SetText("测试失败 " + timeStr + " (" + result + ")")
				} else {
					ui.telegramTestLabel.SetText("待测试")
				}
			}
			ui.telegramVerifiedBotID = b.AccountID

		case b.Channel == "feishu" && b.Status == "bound":
			ui.feishuStatusLabel.SetText("已绑定")
			ui.feishuBoundLabel.SetText(fallbackText(b.DisplayName, b.SenderID))
			if !b.LastTestAt.IsZero() {
				timeStr := b.LastTestAt.Format("15:04")
				if b.ConnectionStatus == "ok" {
					ui.feishuTestLabel.SetText("测试成功 " + timeStr)
				} else if b.ConnectionStatus == "failed" {
					result := b.LastTestResult
					if len(result) > 20 {
						result = result[:20] + "..."
					}
					ui.feishuTestLabel.SetText("测试失败 " + timeStr + " (" + result + ")")
				} else {
					ui.feishuTestLabel.SetText("待测试")
				}
			}
			ui.feishuVerifiedAppID = b.AccountID

		case b.Channel == "wecom" && b.Status == "bound":
			ui.wecomStatusLabel.SetText("已绑定")
			ui.wecomBoundLabel.SetText(fallbackText(b.DisplayName, b.SenderID))
			if !b.LastTestAt.IsZero() {
				timeStr := b.LastTestAt.Format("15:04")
				if b.ConnectionStatus == "ok" {
					ui.wecomTestLabel.SetText("测试成功 " + timeStr)
				} else if b.ConnectionStatus == "failed" {
					result := b.LastTestResult
					if len(result) > 20 {
						result = result[:20] + "..."
					}
					ui.wecomTestLabel.SetText("测试失败 " + timeStr + " (" + result + ")")
				} else {
					ui.wecomTestLabel.SetText("待测试")
				}
			}
			ui.wecomVerifiedBotID = b.AccountID
		}
	}
}

// ===== 机器人 Tab 操作处理 =====

func (ui *UIApp) handleTelegramTest() {
	ui.showResult("处理中", "正在测试 Telegram 连接...", "")
	go func() {
		token := strings.TrimSpace(ui.telegramTokenEdit.Text())
		if token == "" {
			ui.mw.Synchronize(func() {
				ui.showResult("失败", "请先填写 Bot Token", "")
			})
			return
		}

		// 直接调用 Telegram getMe API 验证 token
		botID, err := verifyTelegramToken(token)
		if err != nil {
			ui.mw.Synchronize(func() {
				ui.showResult("失败", "Token 验证失败: "+err.Error(), "请检查 Token 是否正确")
			})
			return
		}

		// 保存凭证到持久化 store
		if err := ui.client.SaveTelegramCredentials(context.Background(), token); err != nil {
			ui.mw.Synchronize(func() {
				ui.showResult("警告", "Token 验证成功，但保存凭证失败: "+err.Error(), "继续配对流程...")
			})
		}

		ui.mw.Synchronize(func() {
			// 保存验证成功的 bot id 和 token
			ui.telegramVerifiedBotID = botID
			ui.telegramVerifiedToken = token
			// 生成 6 位配对码
			ui.telegramPairingCode = generatePairingCode()
			ui.telegramCodeEdit.SetText(pairingCommand(ui.telegramPairingCode))
			ui.showResult("成功", "Token 验证成功！请复制命令并发给 Telegram 机器人", pairingCommand(ui.telegramPairingCode)+"（3分钟内有效）")
			ui.telegramStatusLabel.SetText("等待配对 (3分钟内有效)")
			ui.startTelegramPairing(token, ui.telegramPairingCode)
		})
	}()
}

// verifyTelegramToken 调用 Telegram getMe API 验证 token，返回 bot id
func verifyTelegramToken(token string) (string, error) {
	// 直接用 HTTP 调用，简化处理
	resp, err := httpGet(fmt.Sprintf("https://api.telegram.org/bot%s/getMe", token))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != 200 {
		return "", fmt.Errorf("HTTP %s", resp.Status)
	}

	var data struct {
		Ok     bool `json:"ok"`
		Result struct {
			ID int64 `json:"id"`
		} `json:"result"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}
	if !data.Ok {
		return "", fmt.Errorf("API 返回错误")
	}
	return fmt.Sprintf("%d", data.Result.ID), nil
}

func httpGet(url string) (*http.Response, error) {
	return http.Get(url)
}

func generatePairingCode() string {
	const chars = "ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	r := rand.New(rand.NewSource(time.Now().UnixNano()))
	code := make([]byte, 6)
	for i := range code {
		code[i] = chars[r.Intn(len(chars))]
	}
	return string(code)
}
func pairingCommand(code string) string {
	code = strings.ToUpper(strings.TrimSpace(code))
	if code == "" {
		return ""
	}
	return "/pair " + code
}
func (ui *UIApp) startTelegramPairing(token, code string) {
	if ui.telegramPairingWatcher != nil {
		ui.telegramPairingWatcher.Stop()
		ui.telegramPairingWatcher = nil
	}

	ui.telegramPairingWatcher = notify.NewTelegramPairingWatcher(token, code)

	go func() {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()

		go ui.telegramPairingWatcher.Start(ctx)

		result, ok := <-ui.telegramPairingWatcher.ResultCh()
		if !ok {
			return
		}

		confirmationSent := "unknown"
		if result.Success {
			bindingsPath := notify.BindingsPath(ui.cfg.RootDir)
			store, err := notify.NewStore(bindingsPath)
			if err != nil {
				result.Success = false
				result.Error = "打开绑定存储失败: " + err.Error()
			} else {
				pending := notify.PendingBinding{
					Channel:     "telegram",
					AccountID:   result.AccountID,
					SenderID:    result.SenderID,
					DisplayName: result.DisplayName,
					PairingCode: code,
				}
				if err := store.UpsertPending(pending); err != nil {
					result.Success = false
					result.Error = "保存待绑定信息失败: " + err.Error()
				} else {
					out, err := ui.client.CompleteTelegramBinding(
						context.Background(),
						result.AccountID,
						result.SenderID,
						result.DisplayName,
						code,
					)
					if err != nil {
						result.Success = false
						result.Error = "自动完成绑定失败: " + err.Error()
					} else {
						confirmationSent = extractField(out, "confirmationSent:")
					}
				}
			}
		}

		ui.mw.Synchronize(func() {
			if result.Success {
				ui.telegramPairingCode = code
				ui.telegramVerifiedBotID = result.AccountID
				ui.telegramPairingResult = nil
				ui.telegramCodeEdit.SetText(pairingCommand(code))
				ui.telegramStatusLabel.SetText("已绑定")
				ui.telegramBoundLabel.SetText(result.DisplayName)
				ui.telegramTestLabel.SetText("待测试")

				if confirmationSent == "ok" {
					ui.showResult("成功", "Telegram 已自动完成绑定", "确认消息已发送到 Telegram，请查收。")
				} else {
					ui.showResult("成功", "Telegram 已自动完成绑定", "绑定完成，但确认消息发送失败，请稍后点击「发送测试消息」。")
				}

				ui.reloadStaticViews()
			} else {
				ui.telegramPairingResult = nil
				ui.telegramStatusLabel.SetText("配对失败")
				ui.showResult("失败", result.Error, "请重新点击「测试连接」开始配对")
			}
		})
	}()
}
func (ui *UIApp) startFeishuPairing(appID, appSecret, code string) {
	if ui.feishuPairingWatcher != nil {
		ui.feishuPairingWatcher.Stop()
		ui.feishuPairingWatcher = nil
	}

	ui.feishuPairingWatcher = notify.NewFeishuPairingWatcher(appID, appSecret, code)
	ui.feishuPairingWatcher.Start()

	go func() {
		result, ok := <-ui.feishuPairingWatcher.ResultCh()
		if !ok || result == nil {
			return
		}

		confirmationSent := "unknown"
		if result.Success {
			bindingsPath := notify.BindingsPath(ui.cfg.RootDir)
			store, err := notify.NewStore(bindingsPath)
			if err != nil {
				result.Success = false
				result.Error = "打开绑定存储失败: " + err.Error()
			} else {
				pending := notify.PendingBinding{
					Channel:     "feishu",
					AccountID:   result.AccountID,
					SenderID:    result.SenderID,
					DisplayName: result.DisplayName,
					PairingCode: code,
				}
				if err := store.UpsertPending(pending); err != nil {
					result.Success = false
					result.Error = "保存待绑定信息失败: " + err.Error()
				} else {
					out, err := ui.client.CompleteFeishuBinding(
						context.Background(),
						result.AccountID,
						result.SenderID,
						result.DisplayName,
						code,
					)
					if err != nil {
						result.Success = false
						result.Error = "自动完成绑定失败: " + err.Error()
					} else {
						confirmationSent = extractField(out, "confirmationSent:")
					}
				}
			}
		}

		ui.mw.Synchronize(func() {
			if result.Success {
				ui.feishuPairingCode = code
				ui.feishuVerifiedAppID = result.AccountID
				ui.feishuPairingResult = nil
				ui.feishuCodeEdit.SetText(pairingCommand(code))
				ui.feishuStatusLabel.SetText("已绑定")
				ui.feishuBoundLabel.SetText(result.DisplayName)
				ui.feishuTestLabel.SetText("待测试")

				if confirmationSent == "ok" {
					ui.showResult("成功", "飞书已自动完成绑定", "确认消息已发送到飞书，请查收。")
				} else {
					ui.showResult("成功", "飞书已自动完成绑定", "绑定完成，但确认消息发送失败，请稍后点击「发送测试消息」。")
				}

				ui.reloadStaticViews()
			} else {
				ui.feishuPairingResult = nil
				ui.feishuStatusLabel.SetText("配对失败")
				ui.showResult("失败", result.Error, "请重新点击「测试连接」开始配对")
			}
		})
	}()
}
func (ui *UIApp) startWecomPairing(botID, code string) {
	if ui.wecomPairingWatcher != nil {
		ui.wecomPairingWatcher.Stop()
		ui.wecomPairingWatcher = nil
	}

	ui.wecomPairingWatcher = notify.NewWecomPairingWatcher(botID, code)
	ui.wecomPairingWatcher.Start()

	go func() {
		result, ok := <-ui.wecomPairingWatcher.ResultCh()
		if !ok || result == nil {
			return
		}

		confirmationSent := "unknown"
		if result.Success {
			bindingsPath := notify.BindingsPath(ui.cfg.RootDir)
			store, err := notify.NewStore(bindingsPath)
			if err != nil {
				result.Success = false
				result.Error = "打开绑定存储失败: " + err.Error()
			} else {
				pending := notify.PendingBinding{
					Channel:     "wecom",
					AccountID:   result.AccountID,
					SenderID:    result.SenderID,
					DisplayName: result.DisplayName,
					PairingCode: code,
				}
				if err := store.UpsertPending(pending); err != nil {
					result.Success = false
					result.Error = "保存待绑定信息失败: " + err.Error()
				} else {
					out, err := ui.client.CompleteWecomBinding(
						context.Background(),
						result.AccountID,
						result.SenderID,
						result.DisplayName,
						code,
					)
					if err != nil {
						result.Success = false
						result.Error = "自动完成绑定失败: " + err.Error()
					} else {
						confirmationSent = extractField(out, "confirmationSent:")
					}
				}
			}
		}

		ui.mw.Synchronize(func() {
			if result.Success {
				ui.wecomPairingCode = code
				ui.wecomVerifiedBotID = result.AccountID
				ui.wecomPairingResult = nil
				ui.wecomCodeEdit.SetText(pairingCommand(code))
				ui.wecomStatusLabel.SetText("已绑定")
				ui.wecomBoundLabel.SetText(result.DisplayName)
				ui.wecomTestLabel.SetText("待测试")

				if confirmationSent == "ok" {
					ui.showResult("成功", "企业微信已自动完成绑定", "确认消息已发送到企业微信，请查收。")
				} else {
					ui.showResult("成功", "企业微信已自动完成绑定", "绑定完成，但确认消息发送失败，请稍后点击「发送测试消息」。")
				}

				ui.reloadStaticViews()
			} else {
				ui.wecomPairingResult = nil
				ui.wecomStatusLabel.SetText("配对失败")
				ui.showResult("失败", result.Error, "请重新点击「测试连接」开始配对")
			}
		})
	}()
}
func (ui *UIApp) handleTelegramBinding() {
	ui.showResult("提示", "Telegram 绑定已改为自动完成", "请复制下方 /pair 命令发给机器人，收到后会自动绑定。")
}

func (ui *UIApp) handleTelegramTestMsg() {
	ui.showResult("处理中", "正在发送测试消息...", "")

	// 优先从内存态获取 token
	token := ui.telegramVerifiedToken
	if token == "" {
		token = strings.TrimSpace(ui.telegramTokenEdit.Text())
	}
	// 如果还是没有，尝试从 credentials store 读取
	if token == "" {
		notify.InitCredentialsStore(ui.cfg.RootDir)
		token = notify.GetTelegramToken()
	}
	if token == "" {
		ui.showResult("失败", "请先填写 Bot Token", "")
		return
	}

	var senderID string
	var accountID string

	// 优先从内存态获取 senderID
	if ui.telegramPairingResult != nil && ui.telegramPairingResult.SenderID != "" {
		senderID = ui.telegramPairingResult.SenderID
		accountID = ui.telegramPairingResult.AccountID
	} else {
		// 从持久化文件读取已绑定的 Telegram senderID
		bindingsPath := notify.BindingsPath(ui.cfg.RootDir)
		store, err := notify.NewStore(bindingsPath)
		if err != nil {
			ui.showResult("失败", "请先完成配对绑定", "请先复制 /pair 命令发给机器人，系统会自动完成绑定。")
			return
		}

		bindings := store.ListBindings()
		for _, b := range bindings {
			if b.Channel == "telegram" && b.Status == "bound" {
				senderID = b.SenderID
				accountID = b.AccountID
				break
			}
		}

		if senderID == "" {
			ui.showResult("失败", "请先完成配对绑定", "请先复制 /pair 命令发给机器人，系统会自动完成绑定。")
			return
		}
	}

	// 解析 chatID
	chatID, err := strconv.ParseInt(senderID, 10, 64)
	if err != nil {
		ui.showResult("失败", "无效的 chatID: "+err.Error(), "")
		return
	}

	go func() {
		// 调用 Telegram sendMessage API (POST JSON)
		success, errMsg := notify.SendTelegramMessage(token, chatID, "🔔 OpenClaw Guard 测试消息 - 绑定成功！")

		// 持久化测试结果
		notify.InitCredentialsStore(ui.cfg.RootDir)
		bindingsPath := notify.BindingsPath(ui.cfg.RootDir)
		store, err := notify.NewStore(bindingsPath)
		if err == nil && accountID != "" && senderID != "" {
			if success {
				_ = store.UpdateBindingTestResult("telegram", accountID, senderID, "连接成功", "ok")
			} else {
				_ = store.UpdateBindingTestResult("telegram", accountID, senderID, errMsg, "failed")
			}
		}

		ui.mw.Synchronize(func() {
			if !success {
				ui.showResult("失败", "发送失败: "+errMsg, "请检查 Bot Token 和 chat_id")
				return
			}
			ui.showResult("成功", "测试消息已发送到 Telegram", "请在手机端查看")
			ui.telegramTestLabel.SetText("测试成功 " + time.Now().Format("15:04"))
			// 刷新状态
			ui.reloadStaticViews()
		})
	}()
}

func (ui *UIApp) handleTelegramUnbind() {
	ui.showResult("处理中", "正在解除 Telegram 绑定...", "")
	go func() {
		err := ui.client.UnbindTelegram(context.Background())
		ui.mw.Synchronize(func() {
			if err != nil {
				ui.showResult("失败", "解除绑定失败: "+err.Error(), "请重试")
				return
			}
			ui.showResult("成功", "Telegram 绑定已解除", "")
			ui.telegramStatusLabel.SetText("未绑定")
			ui.telegramBoundLabel.SetText("-")
			ui.telegramTestLabel.SetText("待测试")
			// 清空内存态
			ui.telegramVerifiedBotID = ""
			ui.telegramVerifiedToken = ""
			ui.telegramPairingResult = nil
		})
	}()
}

func (ui *UIApp) handleFeishuTest() {
	ui.showResult("处理中", "正在测试飞书连接...", "")

	go func() {
		appID := strings.TrimSpace(ui.feishuAppIDEdit.Text())
		appSecret := strings.TrimSpace(ui.feishuAppSecretEdit.Text())

		if appID == "" || appSecret == "" {
			ui.mw.Synchronize(func() {
				ui.showResult("失败", "请先填写 App ID 和 App Secret", "")
			})
			return
		}

		if err := notify.VerifyFeishuCredentials(appID, appSecret); err != nil {
			ui.mw.Synchronize(func() {
				ui.showResult("失败", "飞书凭证验证失败: "+err.Error(), "请检查 App ID / App Secret")
			})
			return
		}

		if err := ui.client.SaveFeishuCredentials(context.Background(), appID, appSecret); err != nil {
			ui.mw.Synchronize(func() {
				ui.showResult("警告", "飞书凭证验证成功，但保存失败: "+err.Error(), "继续配对流程...")
			})
		}

		code := generatePairingCode()

		ui.mw.Synchronize(func() {
			ui.feishuPairingCode = code
			ui.feishuVerifiedAppID = appID
			ui.feishuPairingResult = nil
			ui.feishuCodeEdit.SetText(pairingCommand(code))
			ui.feishuStatusLabel.SetText("等待配对 (3分钟内有效)")
			ui.showResult("成功", "飞书凭证验证成功！请复制命令并发给飞书机器人", pairingCommand(code)+"（3分钟内有效）")
			ui.startFeishuPairing(appID, appSecret, code)
		})
	}()
}

func (ui *UIApp) handleFeishuBinding() {
	ui.showResult("提示", "飞书 绑定已改为自动完成", "请复制下方 /pair 命令发给机器人，收到后会自动绑定。")
}

func (ui *UIApp) handleFeishuTestMsg() {
	ui.showResult("处理中", "正在发送飞书测试消息...", "")

	go func() {
		out, err := ui.client.TestFeishuMessage(context.Background(), "")
		ui.mw.Synchronize(func() {
			if err != nil {
				ui.showResult("失败", "发送飞书测试消息失败: "+err.Error(), "请先完成绑定并检查飞书凭证")
				return
			}

			ui.showResult("成功", firstLine(out), "请到飞书查看测试消息。")
			ui.feishuTestLabel.SetText("测试成功 " + time.Now().Format("15:04"))
			ui.reloadStaticViews()
		})
	}()
}

func (ui *UIApp) handleFeishuUnbind() {
	ui.showResult("处理中", "正在解除飞书绑定...", "")

	go func() {
		err := ui.client.UnbindFeishu(context.Background())
		ui.mw.Synchronize(func() {
			if err != nil {
				ui.showResult("失败", "解除飞书绑定失败: "+err.Error(), "请重试")
				return
			}

			ui.showResult("成功", "飞书绑定已解除", "")
			ui.feishuStatusLabel.SetText("未绑定")
			ui.feishuBoundLabel.SetText("-")
			ui.feishuTestLabel.SetText("待测试")
			ui.feishuVerifiedAppID = ""
			ui.feishuPairingCode = ""
			ui.feishuPairingResult = nil
			if ui.feishuPairingWatcher != nil {
				ui.feishuPairingWatcher.Stop()
				ui.feishuPairingWatcher = nil
			}
		})
	}()
}

func (ui *UIApp) handleWecomTest() {
	ui.showResult("处理中", "正在测试企业微信连接...", "")

	go func() {
		botID := strings.TrimSpace(ui.wecomBotIDEdit.Text())
		secret := strings.TrimSpace(ui.wecomSecretEdit.Text())

		if botID == "" || secret == "" {
			ui.mw.Synchronize(func() {
				ui.showResult("失败", "请先填写 Bot ID 和 Secret", "")
			})
			return
		}

		if err := ui.client.SaveWecomCredentials(context.Background(), botID, secret); err != nil {
			ui.mw.Synchronize(func() {
				ui.showResult("失败", "企业微信凭证保存失败: "+err.Error(), "请先修复保存问题，再开始配对")
			})
			return
		}

		code := generatePairingCode()

		if err := notify.EnsureWecomBridge(botID, secret); err != nil {
			ui.mw.Synchronize(func() {
				ui.showResult("失败", "企业微信 bridge 启动失败: "+err.Error(), "请检查 helper 路径、Node 环境，或把命令输出发给我继续核对。")
			})
			return
		}

		ui.mw.Synchronize(func() {
			ui.wecomPairingCode = code
			ui.wecomVerifiedBotID = botID
			ui.wecomPairingResult = nil
			ui.wecomCodeEdit.SetText(pairingCommand(code))
			ui.wecomStatusLabel.SetText("等待配对 (3分钟内有效)")
			ui.showResult("成功", "企业微信凭证已保存！请复制命令并发给企业微信机器人", pairingCommand(code)+"（3分钟内有效）")
			ui.startWecomPairing(botID, code)
		})
	}()
}

func (ui *UIApp) handleWecomBinding() {
	ui.showResult("提示", "企业微信 绑定已改为自动完成", "请复制下方 /pair 命令发给机器人，收到后会自动绑定。")
}

func (ui *UIApp) handleWecomTestMsg() {
	ui.showResult("处理中", "正在发送企业微信测试消息...", "")

	go func() {
		out, err := ui.client.TestWecomMessage(context.Background(), "")
		ui.mw.Synchronize(func() {
			if err != nil {
				ui.showResult("失败", "发送企业微信测试消息失败: "+err.Error(), "请先完成绑定并检查企业微信凭证")
				return
			}

			ui.showResult("成功", firstLine(out), "请到企业微信查看测试消息。")
			ui.wecomTestLabel.SetText("测试成功 " + time.Now().Format("15:04"))
			ui.reloadStaticViews()
		})
	}()
}

func (ui *UIApp) handleWecomUnbind() {
	ui.showResult("处理中", "正在解除企业微信绑定...", "")

	go func() {
		err := ui.client.UnbindWecom(context.Background())
		ui.mw.Synchronize(func() {
			if err != nil {
				ui.showResult("失败", "解除企业微信绑定失败: "+err.Error(), "请重试")
				return
			}

			ui.showResult("成功", "企业微信绑定已解除", "")
			ui.wecomStatusLabel.SetText("未绑定")
			ui.wecomBoundLabel.SetText("-")
			ui.wecomTestLabel.SetText("待测试")
			ui.wecomVerifiedBotID = ""
			ui.wecomPairingCode = ""
			ui.wecomPairingResult = nil
			if ui.wecomPairingWatcher != nil {
				ui.wecomPairingWatcher.Stop()
				ui.wecomPairingWatcher = nil
			}
			notify.StopWecomBridge()
		})
	}()
}

// ===== 维护 Tab 操作处理 =====

func (ui *UIApp) handlePauseGuard() {
	ui.showResult("处理中", "正在暂停监控...", "")

	go func() {
		ctx := context.Background()
		err := ui.client.PauseMonitoring(ctx)
		snapshot := ui.client.Snapshot(ctx)

		ui.mw.Synchronize(func() {
			if err != nil {
				ui.showResult("失败", "暂停监控失败: "+err.Error(), "请检查 .guard-state 目录权限")
				if ui.tray != nil {
					_ = ui.tray.ShowError("OpenClaw Guard", "暂停监控失败，请检查目录权限或守护日志。")
				}
				return
			}

			ui.applySnapshot(snapshot)
			ui.reloadStaticViews()
			ui.showResult("成功", "监控已暂停。", "暂停期间不会自动回滚配置文件。")
			if ui.tray != nil {
				_ = ui.tray.ShowInfo("OpenClaw Guard", "监控已暂停。")
			}
		})
	}()
}

func (ui *UIApp) handleResumeGuard() {
	ui.showResult("处理中", "正在恢复监控...", "")

	go func() {
		ctx := context.Background()
		err := ui.client.ResumeMonitoring(ctx)
		snapshot := ui.client.Snapshot(ctx)

		ui.mw.Synchronize(func() {
			if err != nil {
				ui.showResult("失败", "恢复监控失败: "+err.Error(), "请检查 .guard-state 目录权限")
				if ui.tray != nil {
					_ = ui.tray.ShowError("OpenClaw Guard", "恢复监控失败，请检查目录权限或守护日志。")
				}
				return
			}

			ui.applySnapshot(snapshot)
			ui.reloadStaticViews()
			ui.showResult("成功", "监控已恢复。", "恢复后会继续检测并回滚非法改动。")
			if ui.tray != nil {
				_ = ui.tray.ShowInfo("OpenClaw Guard", "监控已恢复。")
			}
		})
	}()
}

func (ui *UIApp) handleGuardWriteTest() {
	ui.showResult("处理中", "正在测试守护写入链路...", "")
	go func() {
		out, err := ui.client.TestGuardWrite(context.Background())
		ui.mw.Synchronize(func() {
			if err != nil {
				ui.showResult("失败", fmt.Sprintf("守护写入测试失败：%v", err), "请先确认 guard 服务已启动。")
				return
			}
			ui.showResult("成功", firstLine(out), "已完成 request-write 与 complete-write。")
		})
	}()
}

// ===== 通用方法 =====

func (ui *UIApp) openPath(path string) {
	if path == "" {
		ui.showResult("失败", "路径为空，无法打开。", "请先检查 root 或 config 配置。")
		return
	}

	if err := exec.Command("explorer.exe", path).Start(); err != nil {
		ui.showResult("失败", fmt.Sprintf("打开路径失败：%v", err), "请手动打开该目录。")
		return
	}

	ui.showResult("成功", fmt.Sprintf("已打开：%s", path), "")
}

func (ui *UIApp) showPanel() {
	ui.mw.Show()
	ui.mw.SetVisible(true)
	_ = ui.mw.Activate()
}

func (ui *UIApp) showResult(status, detail, hint string) {
	ui.resultStatus.SetText(status)
	ui.resultDetail.SetText(detail)
	ui.resultHint.SetText(hint)
}

func (ui *UIApp) exit() {
	if ui.tray != nil {
		_ = ui.tray.Dispose()
		ui.tray = nil
	}
	if ui.mw != nil {
		_ = ui.mw.Close()
	}
	walk.App().Exit(0)
}

func (ui *UIApp) dispose() {
	notify.StopWecomBridge()
	if ui.icon != nil {
		ui.icon.Dispose()
		ui.icon = nil
	}
	if ui.tray != nil {
		_ = ui.tray.Dispose()
		ui.tray = nil
	}
}

func fallbackText(value, alt string) string {
	if value == "" {
		return alt
	}
	return value
}

func firstLine(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return value
	}
	parts := strings.FieldsFunc(value, func(r rune) bool { return r == '\n' || r == '\r' })
	if len(parts) == 0 {
		return value
	}
	return parts[0]
}
