# OpenClaw Guard Kit

> 鏈枃妗ｇ敱 **openclaw-澶х铔?* 鎬荤粨涓婁紶銆?
[![Platform](https://img.shields.io/badge/platform-Windows-blue?style=flat-square)](#)
[![License](https://img.shields.io/badge/license-MIT-green?style=flat-square)](LICENSE)
[![Go](https://img.shields.io/badge/Go-1.25+-blue?style=flat-square)](#寮€鍙?

---

## 鐩綍锛堜腑鏂囷級

- [蹇€熷紑濮媇(#蹇€熷紑濮?
- [鍔熻兘姒傝](#鍔熻兘姒傝)
- [缁勪欢鏋舵瀯](#缁勪欢鏋舵瀯)
- [鍛戒护琛屾帴鍙(#鍛戒护琛屾帴鍙?
- [杩滅▼瀹夎涓庡崌绾(#杩滅▼瀹夎涓庡崌绾?
- [閰嶇疆璇存槑](#閰嶇疆璇存槑)
- [杩愯鏃舵枃浠禲(#杩愯鏃舵枃浠?
- [寮€鍙慮(#寮€鍙?

> 馃殌 English documentation: [Jump to English](#english-documentation)

---

## 蹇€熷紑濮?
### 鍓嶇疆瑕佹眰

- Windows 鎿嶄綔绯荤粺
- 宸插畨瑁?[OpenClaw](https://github.com/openclaw/openclaw)
- PowerShell 5.0+

### 涓€閿畨瑁?
浠?Gitee锛堝浗鍐呴暅鍍忥級鑷姩涓嬭浇骞跺畨瑁呮渶鏂扮増鏈細

```powershell
irm https://gitee.com/sorry123luck/openclaw-guard-kit/raw/main/installer/install.ps1 | iex
```

> 鍥介檯鐢ㄦ埛鍙娇鐢?GitHub锛堝浗鍐呰闂參锛夛細
> `irm https://github.com/sorry123luck/openclaw-guard-kit/raw/main/installer/install.ps1 | iex`

- 瀹夎璺緞锛歚~/.openclaw-guard-kit/`
- OpenClaw 璺緞锛歚~/.openclaw/`

瀹夎瀹屾垚鍚庯細
- `guard-detector.exe` 鑷姩娉ㄥ唽涓哄紑鏈鸿嚜鍚姩
- `guard-ui.exe` 鍦ㄧ郴缁熸墭鐩樿繍琛?- 妫€娴嬪埌 OpenClaw 涓婄嚎鍚庤嚜鍔ㄦ媺璧峰畧鎶ょ▼搴?
### 鍗囩骇

```powershell
powershell -ExecutionPolicy Bypass -File "$env:USERPROFILE\.openclaw-guard-kit\installer\update.ps1"
```

### 鍗歌浇

```powershell
powershell -ExecutionPolicy Bypass -File "$env:USERPROFILE\.openclaw-guard-kit\installer\uninstall.ps1"
```

鍔?`-RemoveInstallDir` 鍙悓鏃跺垹闄ゅ畨瑁呯洰褰曟湰韬€?
---

## 鍔熻兘姒傝

### 1. 鐢熷懡鍛ㄦ湡鐩戞帶

`guard-detector.exe` 鎸佺画妫€娴?OpenClaw 鍦ㄧ嚎鐘舵€侊細

| OpenClaw 鐘舵€?| 琛屼负 |
|---------------|------|
| 鍦ㄧ嚎 | 鑷姩纭繚 `guard.exe` 鍜?`guard-ui.exe` 杩愯 |
| 绂荤嚎纭 | 鑷姩鍋滄 guard 鍜?UI锛屽彂閫佺绾块€氱煡 |
| 杩滅▼鍚姩鍛戒护 | 杩涘叆 30 绉掑揩閫熸帰娴嬬獥鍙ｏ紝姣忕鎺㈡祴涓€娆?|

### 2. 閰嶇疆鏂囦欢淇濇姢

`guard.exe` 鐩戞帶 OpenClaw 鍏抽敭閰嶇疆鏂囦欢锛?
```
鏂囦欢鍙樺寲 鈫?绋冲畾绛夊緟锛?s锛夆啋 鍒涘缓鍊欓€夊揩鐓?鈫?鍋ュ悍妫€鏌?鈫?Doctor 璇婃柇 鈫?鍒ゅ畾澶勭悊鏂瑰紡
```

| 鍒ゅ畾缁撴灉 | 澶勭悊 |
|----------|------|
| 閰嶇疆姝ｅ父 | 鍊欓€夊崌鏍间负鍙俊鍩虹嚎 |
| 杩愯鏃跺紓甯?| 绛夊緟鑷剤锛屼笉鍥炴粴 |
| 纭晠闅?| 鑷姩鍥炴粴鍒颁笂涓€涓彲淇＄増鏈?|

**鍙椾繚鎶ゆ枃浠讹細**

- `openclaw.json`锛堝綊涓€鍖栨瘮杈冿紝蹇界暐杩愯鏃跺瓧娈碉級
- `auth-profiles.json`锛堜粎姣旇緝 version + profiles锛?- `models.json`锛堝彲閫夛級

### 3. 閫氱煡涓庤繙绋嬪懡浠?
涓変釜骞冲彴閫氶亾锛屾瘡涓€氶亾鐙珛鎺у埗閫氱煡寮€鍏冲拰杩滅▼鍛戒护鏉冮檺锛?
| 骞冲彴 | 鍑瘉 | 缁戝畾鏂瑰紡 |
|------|------|----------|
| Telegram | Bot Token | 鏈哄櫒浜轰細璇濈粦瀹?|
| 椋炰功 | App ID + App Secret | 搴旂敤娑堟伅缁戝畾 |
| 浼佷笟寰俊 | Corp ID + Agent ID + Secret | 浼佷笟搴旂敤浼氳瘽缁戝畾 |

宸茬粦瀹氱敤鎴峰彲鍙戦€佽繙绋嬪懡浠わ細

- `鍚姩openclaw`
- `閲嶅惎openclaw`

---

## 缁勪欢鏋舵瀯

```
鈹屸攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?鈹?                 鐢ㄦ埛 / 鏈哄櫒浜?                        鈹?鈹?             (Telegram 路 椋炰功 路 浼佷笟寰俊)            鈹?鈹斺攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?                       鈹?杩滅▼鍛戒护 / 閫氱煡
鈹屸攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈻尖攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?鈹?                guard-detector.exe                     鈹?鈹? 鈹屸攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?鈹屸攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?鈹屸攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?鈹?鈹? 鈹? 鐢熷懡鍛ㄦ湡鐩戞帶  鈹?鈹? 杩滅▼鍛戒护澶勭悊  鈹?鈹? 蹇€熸帰娴嬬獥鍙? 鈹?鈹?鈹? 鈹斺攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?鈹斺攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?鈹斺攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?鈹?鈹?        鈹?               鈹?                 鈹?         鈹?鈹?   OpenClaw 鍦ㄧ嚎     缁戝畾鏍￠獙          30s/1s 鎺㈡祴     鈹?鈹斺攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?          鈹?鎷夎捣 / 鍋滄                      鈹?TCP 鎺㈡祴
鈹屸攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈻尖攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈻尖攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?鈹?                     guard.exe                          鈹?鈹? 鈹屸攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?鈹屸攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?鈹屸攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹? 鈹?鈹? 鈹?  鏂囦欢鐩戞帶    鈹?鈹?ReviewWorker 鈹?鈹?Backup Service 鈹? 鈹?鈹? 鈹斺攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?鈹斺攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?鈹斺攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹? 鈹?鈹斺攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?          鈹?鈹屸攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈻尖攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?鈹?                guard-ui.exe锛堟墭鐩橈級                        鈹?鈹?  detector / guard / gateway 涓夊眰鐘舵€?路 鍑瘉绠＄悊 路 缁戝畾绠＄悊   鈹?鈹斺攢鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹€鈹?```

---

## 鍛戒护琛屾帴鍙?
> 浠ヤ笅鍒楀嚭 `guard.exe` 鐨勫父鐢ㄥ瓙鍛戒护锛沗guard-detector.exe` 閫氬父鐢卞畨瑁呭櫒/鑷惎鍔ㄦ祦绋嬬鐞嗭紝鏅€氱敤鎴蜂竴鑸棤闇€鎵嬪姩璋冪敤銆?
### 鏍稿績鍛戒护

| 鍛戒护 | 璇存槑 |
|------|------|
| `guard.exe watch` | 鍚姩閰嶇疆鏂囦欢鐩戞帶寰幆 |
| `guard.exe prepare` | 鐢熸垚鍙俊鍩虹嚎蹇収锛坢anifest.json锛?|
| `guard.exe status` | 鏌ョ湅褰撳墠 guard 杩愯鐘舵€?|
| `guard.exe stop` | 鍋滄 watch 寰幆 |

### 鍊欓€夌鐞嗗懡浠?
| 鍛戒护 | 璇存槑 |
|------|------|
| `guard.exe candidate-status` | 鏌ョ湅褰撳墠鍊欓€夌姸鎬?|
| `guard.exe promote-candidate` | 灏嗗€欓€夊崌鏍间负鍙俊 |
| `guard.exe discard-candidate` | 涓㈠純褰撳墠鍊欓€?|
| `guard.exe mark-bad-candidate` | 鏍囪鍊欓€変负鍧忓苟褰掓。 |
| `guard.exe retry-candidate` | 閲嶆柊瀹℃煡褰撳墠鍊欓€?|

### Telegram 鍑瘉涓庣粦瀹?
```bash
guard.exe save-telegram-credentials --token <bot-token>
guard.exe complete-telegram-binding --chat-id <chatId>
guard.exe unbind-telegram
```

### 椋炰功鍑瘉涓庣粦瀹?
```bash
guard.exe save-feishu-credentials --app-id <appId> --app-secret <secret>
guard.exe complete-feishu-binding --open-id <openId>
guard.exe unbind-feishu
guard.exe test-feishu-message --open-id <openId> --content <text>
```

### 浼佷笟寰俊鍑瘉涓庣粦瀹?
```bash
guard.exe save-wecom-credentials --corp-id <corpId> --agent-id <agentId> --secret <secret>
guard.exe test-wecom-connection
guard.exe complete-wecom-binding --user-id <userId>
guard.exe unbind-wecom
guard.exe test-wecom-message --user-id <userId> --content <text>
```

---

## 杩滅▼瀹夎涓庡崌绾?
> 瀹夎鍣ㄩ粯璁や紭鍏堜粠 **Gitee** 涓嬭浇锛孏itee 涓嶅彲鐢ㄦ椂鑷姩鍒囨崲鍒?**GitHub**锛屾棤闇€鎵嬪姩骞查銆?
### 杩滅▼瀹夎锛堟湰鍦版墽琛岋級

**鍥藉唴鐢ㄦ埛锛堜竴閿畨瑁咃級锛?*
```powershell
irm https://gitee.com/sorry123luck/openclaw-guard-kit/raw/main/installer/install.ps1 | iex
```

**鍥介檯鐢ㄦ埛锛?*
```powershell
irm https://github.com/sorry123luck/openclaw-guard-kit/raw/main/installer/install.ps1 | iex
```

**鍙傛暟锛?*

| 鍙傛暟 | 榛樿鍊?| 璇存槑 |
|------|--------|------|
| `-InstallDir` | `~/.openclaw-guard-kit/` | 瀹夎鐩綍 |
| `-OpenClawRoot` | `~/.openclaw/` | OpenClaw 鏍圭洰褰?|
| `-PrimarySource` | `gitee` | 棣栭€夋簮锛屽彲閫?`github` |

### 杩滅▼鍗囩骇

鍗囩骇鑴氭湰鍚屾牱鏀寔鍙屾簮鑷姩鍥為€€锛?```powershell
powershell -ExecutionPolicy Bypass -File "$env:USERPROFILE\.openclaw-guard-kit\installer\update.ps1"
```

鏀寔鍙傛暟锛歚-InstallDir` / `-PrimarySource gitee|github`

### 鎵嬪姩涓嬭浇瀹夎

閫傜敤浜庢棤娉曟墽琛岃繙绋?PowerShell 鑴氭湰鐨勫満鏅細

```powershell
# 1. 涓嬭浇鏈€鏂?zip
# https://github.com/sorry123luck/openclaw-guard-kit/releases/latest/download/openclaw-guard-kit-windows-x64.zip

# 2. 瑙ｅ帇鍚庢湰鍦板畨瑁?powershell -ExecutionPolicy Bypass -File ".\installer\install.ps1"
```

---

## 閰嶇疆璇存槑

### guard-detector 涓昏鍙傛暟

| 鍙傛暟 | 榛樿鍊?| 璇存槑 |
|------|--------|------|
| `--openclaw-path` | 绯荤粺 PATH 涓殑 openclaw | openclaw.exe 璺緞 |
| `--root` | `~/.openclaw/` | OpenClaw 鏍圭洰褰?|
| `--agent` | `main` | Agent ID |
| `--gateway-port` | 0锛堣嚜鍔ㄥ彂鐜帮級 | 鍥哄畾 gateway 绔彛锛?=鑷姩 |
| `--probe-interval` | 5 绉?| 鎺㈡祴闂撮殧 |
| `--startup-protect` | 20 绉?| 鍚姩淇濇姢绐楀彛 |
| `--offline-grace` | 90 绉?| 绂荤嚎瀹介檺鏈?|
| `--restart-cooldown` | 45 绉?| 閲嶅惎鍐峰嵈鏃堕棿 |
| `--healthy-confirm` | 2 娆?| 鍦ㄧ嚎纭娆℃暟 |
| `--unhealthy-confirm` | 3 娆?| 绂荤嚎纭娆℃暟 |

### 閫氶亾鏉冮檺鎺у埗

姣忎釜宸茬粦瀹氶€氶亾鐙珛鎺у埗锛?
| 寮€鍏?| 璇存槑 |
|------|------|
| `notifyEnabled` | 鏄惁鎺ユ敹閫氱煡 |
| `remoteCommandEnabled` | 鏄惁鍏佽杩滅▼鍛戒护 |

---

## 杩愯鏃舵枃浠?
绋嬪簭杩愯鍚庡湪 OpenClaw 鏍圭洰褰曚笅鐢熸垚锛?
```
<OpenClawRoot>\
鈹溾攢鈹€ .guard-state\
鈹?  鈹溾攢鈹€ manifest.json           # 蹇収娓呭崟锛圱rusted + Candidate锛?鈹?  鈹溾攢鈹€ detector-status.json    # detector 鐘舵€?鈹?  鈹溾攢鈹€ gateway-port-cache.json # gateway 绔彛缂撳瓨
鈹?  鈹溾攢鈹€ startup-protect.json    # 鍚姩淇濇姢绐楀彛
鈹?  鈹斺攢鈹€ logs\
鈹?      鈹斺攢鈹€ doctor-*.log        # Doctor 璇婃柇鏃ュ織
鈹斺攢鈹€ .offline                    # 绂荤嚎鏍囪锛堟墜鍔ㄦ斁缃彲寮哄埗绂荤嚎锛?```

---

## 寮€鍙?
### 椤圭洰缁撴瀯

```
openclaw-guard-kit/
鈹溾攢鈹€ cmd/
鈹?  鈹溾攢鈹€ guard/                  # guard 涓荤▼搴?鈹?  鈹溾攢鈹€ guard-detector/         # detector 涓荤▼搴?鈹?  鈹斺攢鈹€ guard-ui/               # UI 绋嬪簭锛堟墭鐩橈級
鈹溾攢鈹€ internal/
鈹?  鈹溾攢鈹€ review/                 # 鍊欓€夊鏌?Worker
鈹?  鈹溾攢鈹€ protocol/               # 鍗忚绫诲瀷瀹氫箟
鈹?  鈹斺攢鈹€ ...
鈹溾攢鈹€ notify/                    # 閫氱煡閫氶亾锛圱elegram / 椋炰功 / 浼佷笟寰俊锛?鈹溾攢鈹€ backup/                    # 蹇収绠＄悊
鈹溾攢鈹€ watch/                     # 鏂囦欢鐩戞帶鏈嶅姟
鈹溾攢鈹€ gateway/                   # Named Pipe IPC
鈹溾攢鈹€ config/                    # 閰嶇疆瑙ｆ瀽
鈹斺攢鈹€ dist/.../installer/        # 鎵撳寘鍚庣殑瀹夎鑴氭湰
```

### 鎶€鏈爤

- Go 1.25+
- `github.com/Microsoft/go-winio` 鈥?Windows Named Pipe
- `github.com/larksuite/oapi-sdk-go/v3` 鈥?椋炰功 SDK
- `github.com/lxn/walk` 鈥?Windows GUI

---

> 馃憜 [杩斿洖涓枃鐩綍](#鐩綍涓枃)

---

## English Documentation

> 鏈珷鑺傜敱 openclaw-澶х铔?鏁寸悊銆?
### Overview

OpenClaw Guard Kit is a **Windows-only** external guardian and recovery tool for OpenClaw. It runs as independent processes and does not modify OpenClaw's source code.

**Core capabilities:**

- Detects OpenClaw online/offline status and auto-spawns/stops guardian processes
- Monitors critical config files with drift detection and candidate review workflow
- Sends notifications and accepts remote commands via Telegram, Feishu, and WeCom
- Supports automatic rollback on config hard failures

### Quick Install

> The installer automatically falls back to GitHub if Gitee is unavailable.

**Recommended (Gitee 鈥?fast in China):**
```powershell
irm https://gitee.com/sorry123luck/openclaw-guard-kit/raw/main/installer/install.ps1 | iex
```

**International (GitHub):**
```powershell
irm https://github.com/sorry123luck/openclaw-guard-kit/raw/main/installer/install.ps1 | iex
```

- Install dir: `~/.openclaw-guard-kit/`
- OpenClaw root: `~/.openclaw/`

### Upgrade

The updater also supports dual-source fallback:
```powershell
powershell -ExecutionPolicy Bypass -File "$env:USERPROFILE\.openclaw-guard-kit\installer\update.ps1"
```

### Uninstall

```powershell
powershell -ExecutionPolicy Bypass -File "$env:USERPROFILE\.openclaw-guard-kit\installer\uninstall.ps1"
```

### Components

| Executable | Role |
|------------|------|
| `guard-detector.exe` | Lifecycle monitor: detects OpenClaw online/offline, spawns/stops guard and UI |
| `guard.exe` | File guardian: monitors config files, runs candidate review workflow |
| `guard-ui.exe` | System tray control panel: status display, credential & binding management |

### CLI Subcommands

**Core:**
- `guard.exe watch` 鈥?Start file monitoring loop
- `guard.exe prepare` 鈥?Generate trusted baseline snapshots
- `guard.exe status` 鈥?Show current guard status
- `guard.exe stop` 鈥?Stop watch loop

**Candidate management:**
- `candidate-status` / `promote-candidate` / `discard-candidate` / `mark-bad-candidate` / `retry-candidate`

**Platform credentials & binding:**
- Telegram: `save-telegram-credentials`, `complete-telegram-binding`, `unbind-telegram`
- Feishu: `save-feishu-credentials`, `complete-feishu-binding`, `unbind-feishu`, `test-feishu-message`
- WeCom: `save-wecom-credentials`, `test-wecom-connection`, `complete-wecom-binding`, `unbind-wecom`, `test-wecom-message`

### Remote Install Options

**One-liner 鈥?Gitee (China, recommended):**
```powershell
irm https://gitee.com/sorry123luck/openclaw-guard-kit/raw/main/installer/install.ps1 | iex
```

**One-liner 鈥?GitHub (international):**
```powershell
irm https://github.com/sorry123luck/openclaw-guard-kit/raw/main/installer/install.ps1 | iex
```

Both installers automatically fall back to the other source if the primary is unavailable.

**With custom paths:**
```powershell
irm https://gitee.com/sorry123luck/openclaw-guard-kit/raw/main/installer/install.ps1 | iex `
  -InstallDir "D:\guard-kit" `
  -OpenClawRoot "D:\openclaw"
```

**Manual download:**
```powershell
# Gitee:
# https://gitee.com/sorry123luck/openclaw-guard-kit/releases/download/v.1.0.0/openclaw-guard-kit-windows-x64.zip
# GitHub:
# https://github.com/sorry123luck/openclaw-guard-kit/releases/download/v.1.0.0/openclaw-guard-kit-windows-x64.zip

powershell -ExecutionPolicy Bypass -File ".\installer\install.ps1"
```

### Runtime Files

```
<OpenClawRoot>\.guard-state\
鈹溾攢鈹€ manifest.json           # Snapshot manifest (Trusted + Candidate)
鈹溾攢鈹€ detector-status.json    # Detector state
鈹溾攢鈹€ gateway-port-cache.json # Cached gateway port
鈹溾攢鈹€ startup-protect.json   # Startup protection window
鈹斺攢鈹€ logs\
    鈹斺攢鈹€ doctor-*.log        # Doctor diagnosis logs
```

### License

MIT
