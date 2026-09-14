export interface AtelierDesktopAPI {
  speechToTextSupported: boolean;
  startTranscription(): Promise<void>;
  appendTranscriptionAudio(audio: ArrayBuffer): void;
  stopTranscription(): Promise<void>;
  onPushToTalk(handler: (pressed: boolean) => void): () => void;
  onTranscriptionDelta(handler: (delta: string) => void): () => void;
  onTranscriptionError(handler: (message: string) => void): () => void;
}

declare global {
  interface Window {
    atelier?: AtelierDesktopAPI;
  }
}

export {};
