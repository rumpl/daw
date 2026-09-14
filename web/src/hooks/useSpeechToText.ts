import { useCallback, useEffect, useRef, useState } from 'react';

export type SpeechToTextState = 'idle' | 'recording' | 'transcribing';

const TARGET_SAMPLE_RATE = 24_000;

function pcm16Buffer(input: Float32Array, inputRate: number): ArrayBuffer {
  const ratio = inputRate / TARGET_SAMPLE_RATE;
  const length = Math.max(1, Math.floor(input.length / ratio));
  const output = new Int16Array(length);
  for (let index = 0; index < length; index += 1) {
    const start = Math.floor(index * ratio);
    const end = Math.max(start + 1, Math.floor((index + 1) * ratio));
    let sample = 0;
    for (let cursor = start; cursor < end && cursor < input.length; cursor += 1) sample += input[cursor] ?? 0;
    sample = Math.max(-1, Math.min(1, sample / Math.max(1, end - start)));
    output[index] = sample < 0 ? sample * 0x8000 : sample * 0x7fff;
  }
  return output.buffer;
}

export function useSpeechToText(onTranscript: (text: string) => void) {
  const [state, setState] = useState<SpeechToTextState>('idle');
  const [error, setError] = useState('');
  const stream = useRef<MediaStream | null>(null);
  const source = useRef<MediaStreamAudioSourceNode | null>(null);
  const context = useRef<AudioContext | null>(null);
  const processor = useRef<ScriptProcessorNode | null>(null);
  const transcript = useRef('');
  const active = useRef(false);
  const onTranscriptRef = useRef(onTranscript);
  onTranscriptRef.current = onTranscript;

  const release = useCallback(() => {
    processor.current?.disconnect();
    processor.current = null;
    source.current?.disconnect();
    source.current = null;
    void context.current?.close();
    context.current = null;
    stream.current?.getTracks().forEach((track) => track.stop());
    stream.current = null;
  }, []);

  const stop = useCallback(() => {
    if (!active.current) return;
    active.current = false;
    setState('transcribing');
    release();
    void window.atelier?.stopTranscription()
      .catch((cause) => setError(cause instanceof Error ? cause.message : 'Failed to stop transcription'))
      .finally(() => setState('idle'));
  }, [release]);

  const start = useCallback(async () => {
    const api = window.atelier;
    if (!api?.speechToTextSupported || active.current) return;
    active.current = true;
    setState('recording');
    setError('');
    transcript.current = '';
    try {
      await api.startTranscription();
      if (!active.current) {
        void api.stopTranscription();
        return;
      }
      const media = await navigator.mediaDevices.getUserMedia({ audio: { channelCount: 1 } });
      if (!active.current) {
        media.getTracks().forEach((track) => track.stop());
        void api.stopTranscription();
        return;
      }
      stream.current = media;
      const audioContext = new AudioContext();
      context.current = audioContext;
      const mediaSource = audioContext.createMediaStreamSource(media);
      source.current = mediaSource;
      const node = audioContext.createScriptProcessor(4096, 1, 1);
      processor.current = node;
      node.onaudioprocess = (event) => {
        api.appendTranscriptionAudio(pcm16Buffer(event.inputBuffer.getChannelData(0), audioContext.sampleRate));
      };
      mediaSource.connect(node);
      node.connect(audioContext.destination);
    } catch (cause) {
      active.current = false;
      release();
      void api.stopTranscription();
      setError(cause instanceof Error ? cause.message : 'Microphone access was denied');
      setState('idle');
    }
  }, [release]);

  useEffect(() => {
    const api = window.atelier;
    if (!api) return;
    const removeDelta = api.onTranscriptionDelta((delta) => {
      transcript.current += delta;
      onTranscriptRef.current(transcript.current);
    });
    const removeError = api.onTranscriptionError((message) => {
      setError(message);
      active.current = false;
      release();
      setState('idle');
    });
    return () => { removeDelta(); removeError(); };
  }, [release]);

  useEffect(() => () => {
    active.current = false;
    release();
    void window.atelier?.stopTranscription();
  }, [release]);

  return {
    supported: Boolean(window.atelier?.speechToTextSupported && typeof AudioContext !== 'undefined'),
    state,
    error,
    start,
    stop,
  };
}
