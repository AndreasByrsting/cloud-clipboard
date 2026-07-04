window.CloudClipboardApi = {
  async request(config) {
    const response = await fetch(config.url, {
      method: config.method || 'GET',
      headers: {
        ...(config.body instanceof FormData ? {} : { 'Content-Type': 'application/json' }),
        ...(config.headers || {}),
      },
      body: config.body instanceof FormData ? config.body : config.body ? JSON.stringify(config.body) : undefined,
      credentials: 'same-origin',
    });
    let data = null;
    const contentType = response.headers.get('content-type') || '';
    if (contentType.includes('application/json')) {
      data = await response.json();
    }
    if (!response.ok) {
      throw new Error(data?.error || response.statusText || 'request failed');
    }
    return data;
  },

  // Room
  listRooms() { return this.request({ url: '/api/rooms' }); },
  createRoom() { return this.request({ method: 'POST', url: '/api/rooms' }); },
  getRoom(roomCode) { return this.request({ url: `/api/rooms/${roomCode}/detail` }); },
  updateRoomName(roomCode, roomName) { return this.request({ method: 'POST', url: `/api/rooms/${roomCode}/name`, body: { roomName } }); },
  extendRoom(roomCode) { return this.request({ method: 'POST', url: `/api/rooms/${roomCode}/extend` }); },
  setPermanentRoom(roomCode) { return this.request({ method: 'POST', url: `/api/rooms/${roomCode}/permanent` }); },
  deleteRoom(roomCode) { return this.request({ method: 'DELETE', url: `/api/rooms/${roomCode}` }); },

  // Messages
  listMessages(roomCode) { return this.request({ url: `/api/rooms/${roomCode}/messages` }); },
  sendText(roomCode, text) { return this.request({ method: 'POST', url: `/api/rooms/${roomCode}/messages`, body: { text } }); },
  async createUploadSession(roomCode, file) {
    return this.request({ method: 'POST', url: `/api/rooms/${roomCode}/uploads`, body: { fileName: file.name, fileSize: file.size, mimeType: file.type || 'application/octet-stream' } });
  },
  uploadChunk(uploadId, chunk) {
    return new Promise((resolve, reject) => {
      const xhr = new XMLHttpRequest();
      xhr.open('PUT', `/api/messages/uploads/${uploadId}/chunk`, true);
      xhr.withCredentials = true;
      xhr.onreadystatechange = () => {
        if (xhr.readyState !== 4) return;
        let data = null;
        try { data = xhr.responseText ? JSON.parse(xhr.responseText) : null; } catch (_) {}
        if (xhr.status >= 200 && xhr.status < 300) resolve(data);
        else reject(new Error(data?.error || xhr.statusText || 'request failed'));
      };
      xhr.onerror = () => reject(new Error('network error'));
      xhr.send(chunk);
    });
  },
  finalizeUpload(uploadId) {
    return this.request({ method: 'POST', url: `/api/messages/uploads/${uploadId}/complete` });
  },
  uploadFile(roomCode, file, callbacks) {
    return new Promise(async (resolve, reject) => {
      try {
        const session = await this.createUploadSession(roomCode, file);
        const chunkSize = session.chunkSize || 5 * 1024 * 1024;
        let uploaded = 0;
        for (let offset = 0; offset < file.size; offset += chunkSize) {
          const chunk = file.slice(offset, offset + chunkSize);
          await this.uploadChunk(session.id, chunk);
          uploaded += chunk.size;
          if (callbacks?.onProgress) callbacks.onProgress(uploaded, file.size);
        }
        const data = await this.finalizeUpload(session.id);
        resolve(data);
      } catch (err) {
        reject(err);
      }
    });
  },
  listEvents(roomCode, sinceEventId) {
    const p = new URLSearchParams(); if (sinceEventId) p.set('since_event_id', String(sinceEventId));
    return this.request({ url: `/api/rooms/${roomCode}/events?${p.toString()}` });
  },

  // Message actions
  deleteMessage(id) { return this.request({ method: 'DELETE', url: `/api/messages/${id}` }); },
  togglePin(id) { return this.request({ method: 'POST', url: `/api/messages/${id}/pin` }); },
  downloadFile(messageId) { return `/api/messages/${messageId}/file`; },

  // Public bootstrap
  bootstrapStatus() { return this.request({ url: '/api/bootstrap/status' }); },
  getPublicSettings() { return this.request({ url: '/api/bootstrap/settings' }); },

  // Admin
  getSession() { return this.request({ url: '/api/admin/session' }); },
  login(password) { return this.request({ method: 'POST', url: '/api/admin/login', body: { password } }); },
  logout() { return this.request({ method: 'POST', url: '/api/admin/logout' }); },
  adminChangePassword(newPassword, confirmPassword) {
    return this.request({ method: 'POST', url: '/api/admin/password', body: { newPassword, confirmPassword } });
  },
  listAdminRooms(page, pageSize) {
    const p = new URLSearchParams();
    if (page) p.set('page', String(page));
    if (pageSize) p.set('pageSize', String(pageSize));
    const qs = p.toString();
    return this.request({ url: `/api/admin/rooms${qs ? '?' + qs : ''}` });
  },
  adminDeleteRoom(roomCode) { return this.request({ method: 'DELETE', url: `/api/admin/rooms/${roomCode}/delete` }); },
  adminBatchDeleteRooms(roomCodes) { return this.request({ method: 'DELETE', url: '/api/admin/rooms', body: { roomCodes } }); },
  adminGetSettings() { return this.request({ url: '/api/admin/settings' }); },
  adminUpdateSettings(settings) { return this.request({ method: 'PUT', url: '/api/admin/settings', body: settings }); },
};
