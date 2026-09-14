const { contextBridge, ipcRenderer } = require('electron');

contextBridge.exposeInMainWorld('atelier', {
  speechToTextSupported: process.platform === 'darwin',
  startTranscription: () => ipcRenderer.invoke('speech-to-text:start'),
  appendTranscriptionAudio: (audio) => ipcRenderer.send('speech-to-text:audio', audio),
  stopTranscription: () => ipcRenderer.invoke('speech-to-text:stop'),
  onPushToTalk: (handler) => {
    const listener = (_event, pressed) => handler(pressed);
    ipcRenderer.on('speech-to-text:push-to-talk', listener);
    return () => ipcRenderer.removeListener('speech-to-text:push-to-talk', listener);
  },
  onTranscriptionDelta: (handler) => {
    const listener = (_event, delta) => handler(delta);
    ipcRenderer.on('speech-to-text:delta', listener);
    return () => ipcRenderer.removeListener('speech-to-text:delta', listener);
  },
  onTranscriptionError: (handler) => {
    const listener = (_event, message) => handler(message);
    ipcRenderer.on('speech-to-text:error', listener);
    return () => ipcRenderer.removeListener('speech-to-text:error', listener);
  },
});
