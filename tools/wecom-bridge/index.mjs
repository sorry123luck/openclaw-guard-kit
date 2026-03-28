#!/usr/bin/env node

import readline from "node:readline";
import process from "node:process";
import AiBot from "@wecom/aibot-node-sdk";

const rl = readline.createInterface({
  input: process.stdin,
  crlfDelay: Infinity,
});

let bridgeState = {
  started: false,
  botId: "",
  secret: "",
  wsClient: null,
  readySent: false,
};

function writeEvent(obj) {
  process.stdout.write(JSON.stringify(obj) + "\n");
}

function writeError(message, requestId = "") {
  writeEvent({
    type: "error",
    requestId,
    error: String(message || "unknown error"),
  });
}

function writeReady(botId = "") {
  writeEvent({
    type: "ready",
    botId,
    ok: true,
    message: "wecom bridge ready",
  });
}

function writeSent(requestId, ok, message = "", error = "") {
  writeEvent({
    type: "sent",
    requestId,
    ok: !!ok,
    message: message || "",
    error: error || "",
  });
}

function writeInboundMessage({
  botId = "",
  userId = "",
  displayName = "",
  chatType = "dm",
  msgType = "text",
  content = "",
  raw = "",
}) {
  writeEvent({
    type: "message",
    botId,
    userId,
    displayName,
    chatType,
    msgType,
    content,
    raw,
  });
}

function currentClient() {
  return bridgeState.wsClient;
}

function disconnectCurrentClient() {
  const client = currentClient();
  bridgeState.wsClient = null;
  if (!client) {
    return;
  }

  try {
    client.disconnect();
  } catch {
    // ignore
  }
}

function setupClient(botId, secret) {
  const wsClient = new AiBot.WSClient({
    botId,
    secret,
  });

  wsClient.on("authenticated", () => {
    if (!bridgeState.readySent) {
      bridgeState.readySent = true;
      writeReady(botId);
    }
  });

  wsClient.on("message.text", (frame) => {
    try {
      const body = frame?.body || {};
      const text = String(body?.text?.content || "").trim();
      if (!text) {
        return;
      }

      const userId = String(body?.from?.userid || "").trim();
      const chatType = body?.chattype === "group" ? "group" : "dm";

      writeInboundMessage({
        botId,
        userId,
        // 官方文档片段里明确有 from.userid，但没有可靠展示昵称字段；
        // 这里先用 userid 占位，后面如果你确认真实 payload 有昵称再补。
        displayName: userId,
        chatType,
        msgType: "text",
        content: text,
        raw: JSON.stringify(frame),
      });
    } catch (err) {
      writeError(`failed to handle inbound text message: ${err?.message || String(err)}`);
    }
  });

  wsClient.on("error", (err) => {
    writeError(`wecom ws error: ${err?.message || String(err)}`);
  });

  wsClient.on("disconnected", () => {
    // 不主动退出，让 SDK 自己按策略重连；
    // 这里只打一个事件，方便 Go 侧看到问题。
    writeError("wecom ws disconnected");
  });

  return wsClient;
}

async function sendTextViaWecom({ botId, secret, userId, text }) {
  if (!botId) {
    throw new Error("missing botId");
  }
  if (!secret) {
    throw new Error("missing secret");
  }
  if (!userId) {
    throw new Error("missing userId");
  }
  if (!text) {
    throw new Error("missing text");
  }

  const wsClient = currentClient();
  if (!wsClient) {
    throw new Error("bridge not started");
  }

  await wsClient.sendMessage(userId, {
    msgtype: "markdown",
    markdown: {
      content: text,
    },
  });

  return {
    ok: true,
    message: "message sent via wecom ws",
  };
}

async function handleStart(cmd) {
  const botId = String(cmd.botId || "").trim();
  const secret = String(cmd.secret || "").trim();

  if (!botId) {
    throw new Error("start requires botId");
  }
  if (!secret) {
    throw new Error("start requires secret");
  }

  disconnectCurrentClient();

  bridgeState.started = true;
  bridgeState.botId = botId;
  bridgeState.secret = secret;
  bridgeState.readySent = false;
  bridgeState.wsClient = setupClient(botId, secret);

  bridgeState.wsClient.connect();
}

async function handleSendText(cmd) {
  const requestId = String(cmd.requestId || "").trim();
  const userId = String(cmd.userId || "").trim();
  const text = String(cmd.text || "").trim();

  if (!bridgeState.started) {
    writeSent(requestId, false, "", "bridge not started");
    return;
  }

  try {
    const result = await sendTextViaWecom({
      botId: bridgeState.botId,
      secret: bridgeState.secret,
      userId,
      text,
    });

    writeSent(
      requestId,
      !!result?.ok,
      String(result?.message || ""),
      result?.ok ? "" : String(result?.error || "send failed"),
    );
  } catch (err) {
    writeSent(requestId, false, "", err?.message || String(err));
  }
}

async function handleStop() {
  disconnectCurrentClient();
  bridgeState.started = false;
  bridgeState.botId = "";
  bridgeState.secret = "";
  bridgeState.readySent = false;
  process.exit(0);
}

rl.on("line", async (line) => {
  const raw = String(line || "").trim();
  if (!raw) {
    return;
  }

  let cmd;
  try {
    cmd = JSON.parse(raw);
  } catch {
    writeError("invalid json command");
    return;
  }

  const type = String(cmd.type || "").trim();

  try {
    switch (type) {
      case "start":
        await handleStart(cmd);
        break;

      case "send_text":
        await handleSendText(cmd);
        break;

      case "stop":
        await handleStop();
        break;

      default:
        writeError(`unknown command type: ${type || "(empty)"}`, cmd.requestId || "");
        break;
    }
  } catch (err) {
    writeError(err?.message || String(err), cmd.requestId || "");
  }
});

rl.on("close", () => {
  disconnectCurrentClient();
  process.exit(0);
});

process.on("SIGINT", () => {
  disconnectCurrentClient();
  process.exit(0);
});

process.on("SIGTERM", () => {
  disconnectCurrentClient();
  process.exit(0);
});