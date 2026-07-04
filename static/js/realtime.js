window.CloudClipboardRealtime = (function () {
  const CONNECT_TIMEOUT = 1800;
  const POLL_MIN_INTERVAL = 1500;
  const POLL_MAX_INTERVAL = 12000;
  const RECONNECT_BASE_INTERVAL = 1500;
  const RECONNECT_MAX_INTERVAL = 30000;

  function nextDelay(base, max, attempt) {
    const exp = Math.min(max, base * Math.pow(2, attempt));
    const jitter = Math.floor(Math.random() * 400);
    return exp + jitter;
  }

  return {
    connect(ctx) {
      const roomCode = ctx.currentRoom?.roomCode;
      if (!roomCode) return;

      this.disconnect(ctx);
      ctx.reconnectAttempt = 0;
      ctx.pollAttempt = 0;
      ctx.pollInFlight = false;
      ctx.setConnectionStatus('connecting');
      this._tryWebSocket(ctx, false);
    },

    _tryWebSocket(ctx, silent) {
      const roomCode = ctx.currentRoom?.roomCode;
      if (!roomCode) return;

      if (ctx.ws) {
        try { ctx.ws.close(1000, 'reconnect'); } catch (_) {}
        ctx.ws = null;
      }

      const protocol = window.location.protocol === 'https:' ? 'wss:' : 'ws:';
      const wsUrl = `${protocol}//${window.location.host}/ws/rooms/${roomCode}`;

      if (!silent) {
        ctx.setConnectionStatus('connecting');
        ctx.banner('');
      }

      try {
        const socket = new WebSocket(wsUrl);
        ctx.ws = socket;

        const connectTimeout = setTimeout(() => {
          if (socket.readyState !== WebSocket.OPEN) {
            try { socket.close(); } catch (_) {}
            if (ctx.ws === socket) {
              ctx.ws = null;
              if (!silent) {
                ctx.setConnectionStatus('error');
              }
              this._fallbackToPolling(ctx);
            }
          }
        }, CONNECT_TIMEOUT);

        socket.onopen = () => {
          clearTimeout(connectTimeout);
          if (ctx.ws !== socket) return;
          ctx.reconnectAttempt = 0;
          ctx.pollAttempt = 0;
          ctx.setConnectionStatus('websocket');
          if (ctx.pollTimeout) {
            clearTimeout(ctx.pollTimeout);
            ctx.pollTimeout = null;
          }
          if (ctx.reconnectTimeout) {
            clearTimeout(ctx.reconnectTimeout);
            ctx.reconnectTimeout = null;
          }
        };

        socket.onmessage = (event) => {
          try {
            const data = JSON.parse(event.data);
            if (data.type === 'welcome') {
              ctx.lastEventId = data.lastEventId || ctx.lastEventId;
            } else if (data.type === 'room_deleted' || data.type === 'room_closed' || data.type === 'room_reopened') {
              if (typeof ctx.applyRoomEvent === 'function') {
                ctx.applyRoomEvent(data);
              }
            } else if (data.type === 'message_deleted') {
              ctx.lastEventId = Math.max(ctx.lastEventId, data.eventId || data.id || 0);
              if (typeof ctx.removeMessage === 'function') {
                ctx.removeMessage(data.messageId);
              } else {
                ctx.refreshMessages();
              }
            } else if (data.type === 'message_pinned' || data.type === 'message_unpinned') {
              ctx.lastEventId = Math.max(ctx.lastEventId, data.eventId || data.id || 0);
              if (typeof ctx.applyMessageEvent === 'function') {
                ctx.applyMessageEvent(data);
              } else {
                ctx.refreshMessages();
              }
            } else if (data.type === 'message' || data.type === 'message_created' || data.type === 'file_created') {
              ctx.lastEventId = Math.max(ctx.lastEventId, data.eventId || data.id || 0);
              if (typeof ctx.applyMessageEvent === 'function') {
                ctx.applyMessageEvent(data);
              } else {
                ctx.refreshMessages();
              }
            }
          } catch (_) {}
        };

        socket.onclose = (event) => {
          clearTimeout(connectTimeout);
          if (ctx.ws === socket) {
            ctx.ws = null;
            if (event.code !== 1000) {
              this._fallbackToPolling(ctx);
            }
          }
        };

        socket.onerror = () => {
          clearTimeout(connectTimeout);
          if (ctx.ws === socket) {
            ctx.ws = null;
            if (!silent) {
              ctx.setConnectionStatus('error');
            }
            try { socket.close(); } catch (_) {}
            this._fallbackToPolling(ctx);
          }
        };
      } catch (_) {
        if (!silent) {
          ctx.setConnectionStatus('error');
        }
        this._fallbackToPolling(ctx);
      }
    },

    // 进入轮询/重连流程，但不立即改状态——等轮询结果决定
    _fallbackToPolling(ctx) {
      this._schedulePolling(ctx, true);
      this._scheduleReconnect(ctx);
    },

    _schedulePolling(ctx, immediate = false) {
      if (ctx.pollTimeout || ctx.pollInFlight) return;
      const delay = immediate ? 0 : nextDelay(POLL_MIN_INTERVAL, POLL_MAX_INTERVAL, ctx.pollAttempt || 0);
      ctx.pollTimeout = setTimeout(async () => {
        ctx.pollTimeout = null;
        const roomCode = ctx.currentRoom?.roomCode;
        if (!roomCode) {
          this.disconnect(ctx);
          return;
        }
        ctx.pollInFlight = true;
        try {
          const api = window.CloudClipboardApi;
          const data = await api.listEvents(roomCode, ctx.lastEventId);
          const events = data.events || [];
          if (events.length > 0) {
            ctx.lastEventId = Math.max(ctx.lastEventId, events[events.length - 1].id);
            ctx.pollAttempt = 0;
            for (const event of events) {
              if (event.type === 'room_deleted' || event.type === 'room_closed' || event.type === 'room_reopened') {
                if (typeof ctx.applyRoomEvent === 'function') {
                  await ctx.applyRoomEvent(event);
                }
                continue;
              }
              if (event.type === 'message_deleted') {
                if (typeof ctx.removeMessage === 'function' && event.messageId) {
                  ctx.removeMessage(event.messageId);
                } else {
                  await ctx.refreshMessages();
                }
                continue;
              }
              if (typeof ctx.applyMessageEvent === 'function') {
                await ctx.applyMessageEvent(event);
              } else {
                await ctx.refreshMessages();
              }
            }
          } else {
            ctx.pollAttempt = Math.min((ctx.pollAttempt || 0) + 1, 6);
          }
          // 轮询成功后才显示"轮询连接"
          if (ctx.connectionStatus !== 'websocket') {
            ctx.setConnectionStatus('polling');
          }
        } catch (_) {
          ctx.pollAttempt = Math.min((ctx.pollAttempt || 0) + 1, 6);
          // 轮询失败显示"连接异常"
          if (ctx.connectionStatus !== 'websocket') {
            ctx.setConnectionStatus('error');
          }
        } finally {
          ctx.pollInFlight = false;
          if (!ctx.ws || ctx.ws.readyState !== WebSocket.OPEN) {
            this._schedulePolling(ctx);
          }
        }
      }, delay);
    },

    _scheduleReconnect(ctx) {
      if (ctx.reconnectTimeout) return;
      const delay = nextDelay(RECONNECT_BASE_INTERVAL, RECONNECT_MAX_INTERVAL, ctx.reconnectAttempt || 0);
      ctx.reconnectTimeout = setTimeout(() => {
        ctx.reconnectTimeout = null;
        if (ctx.ws && ctx.ws.readyState === WebSocket.OPEN) {
          return;
        }
        if (!ctx.currentRoom?.roomCode) {
          return;
        }
        ctx.reconnectAttempt = Math.min((ctx.reconnectAttempt || 0) + 1, 8);
        // 静默重连：失败不改变状态，只有成功才切换到 websocket
        this._tryWebSocket(ctx, true);
        if (!ctx.ws || ctx.ws.readyState !== WebSocket.OPEN) {
          this._scheduleReconnect(ctx);
        }
      }, delay);
    },

    disconnect(ctx) {
      if (ctx.ws) {
        try { ctx.ws.close(1000, 'disconnect'); } catch (_) {}
        ctx.ws = null;
      }
      if (ctx.pollTimeout) {
        clearTimeout(ctx.pollTimeout);
        ctx.pollTimeout = null;
      }
      if (ctx.reconnectTimeout) {
        clearTimeout(ctx.reconnectTimeout);
        ctx.reconnectTimeout = null;
      }
      ctx.pollInFlight = false;
      ctx.pollAttempt = 0;
      ctx.reconnectAttempt = 0;
    },
  };
})();