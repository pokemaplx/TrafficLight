// @ts-check
import { renderEmpty } from "./list.js";
import { state } from "./state.js";
import { toast, ui } from "./ui.js";

let socket = null;
let reconnectDelay = 500;

export function connect(onMessage) {
  const url = new URL("ws", location.href);
  url.protocol = location.protocol === "https:" ? "wss:" : "ws:";
  socket = new WebSocket(url);
  socket.addEventListener("open", () => {
    reconnectDelay = 500;
    setConnected(true);
  });
  socket.addEventListener("message", (event) => onMessage(JSON.parse(event.data)));
  socket.addEventListener("close", () => {
    setConnected(false);
    // a session that ran out, or a new password, can't connect anymore
    fetch("api/session")
      .then((response) => {
        if (response.status === 401) location.assign("login");
      })
      .catch(() => {});
    setTimeout(() => connect(onMessage), reconnectDelay);
    reconnectDelay = Math.min(reconnectDelay * 2, 5000);
  });
}

export function send(command) {
  if (socket?.readyState !== WebSocket.OPEN) {
    toast("Not connected to Traffic Light");
    return;
  }
  socket.send(JSON.stringify(command));
}

export function setConnected(connected) {
  state.connected = connected;
  ui.conn.classList.toggle("connected", connected);
  ui.conn.classList.toggle("disconnected", !connected);
  ui.conn.querySelector(".label").textContent = connected ? "Connected" : "Reconnecting…";
  renderEmpty();
}
