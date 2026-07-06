import { MutableRefObject, useEffect, useRef, useState } from 'react';
import { Spin } from 'antd';
import clsx from 'clsx';
import { useAtom, useAtomValue, useSetAtom } from 'jotai';

import * as api from '@/api/stream.ts';
import { VideoStatus } from '@/types';
import { microphoneEnabledAtom } from '@/jotai/audio.ts';
import { mouseStyleAtom } from '@/jotai/mouse.ts';
import { videoParametersAtom, videoStatusAtom, videoVolumeAtom } from '@/jotai/screen.ts';

interface WebRTCMessage {
  event: string;
  data: string;
}

const createAnswerHandler = (
  connection: RTCPeerConnection,
  offerSentRef: MutableRefObject<boolean>,
  candidatesRef: MutableRefObject<RTCIceCandidate[]>,
  type: 'video' | 'audio'
) => {
  return (data: any) => {
    if (connection.signalingState !== 'have-local-offer') {
      offerSentRef.current = false;
      console.warn(`${type} signaling state incorrect for answer: ${connection.signalingState}`);
      return;
    }

    connection
      .setRemoteDescription(new RTCSessionDescription(data))
      .then(() => {
        offerSentRef.current = false;
        candidatesRef.current.forEach((candidate) => {
          connection
            .addIceCandidate(candidate)
            .catch((e) => console.error(`${type} candidate failed to add:`, e.message));
        });
        candidatesRef.current = [];
      })
      .catch((error) => {
        console.error(`${type} answer set failed:`, error);
        offerSentRef.current = false;
      });
  };
};

const createCandidateHandler = (
  connection: RTCPeerConnection,
  candidatesRef: MutableRefObject<RTCIceCandidate[]>,
  type: 'video' | 'audio'
) => {
  return (data: any) => {
    if (!data.candidate) {
      return;
    }

    const candidate = new RTCIceCandidate(data);
    if (connection.remoteDescription) {
      connection
        .addIceCandidate(candidate)
        .catch((e) => console.error(`${type} candidate failed to add:`, e.message));
    } else {
      candidatesRef.current.push(candidate);
    }
  };
};

export const H264Webrtc = ({ onFailure }: { onFailure?: () => void }) => {
  const videoParameters = useAtomValue(videoParametersAtom);
  const mouseStyle = useAtomValue(mouseStyleAtom);
  const setVideoStatus = useSetAtom(videoStatusAtom);
  const [volume, setVolume] = useAtom(videoVolumeAtom);
  const [micEnabled, setMicEnabled] = useAtom(microphoneEnabledAtom);

  const [isPlaying, setIsPlaying] = useState(true);
  const [isLoading, setIsLoading] = useState(true);

  const videoRef = useRef<HTMLVideoElement | null>(null);
  const audioRef = useRef<HTMLAudioElement | null>(null);

  const videoOfferSent = useRef(false);
  const audioOfferSent = useRef(false);

  const videoIceCandidates = useRef<RTCIceCandidate[]>([]);
  const audioIceCandidates = useRef<RTCIceCandidate[]>([]);

  // References for microphone management
  const audioConnectionRef = useRef<RTCPeerConnection | null>(null);
  const micSenderRef = useRef<RTCRtpSender | null>(null);
  const micStreamRef = useRef<MediaStream | null>(null);
  const micTrackRef = useRef<MediaStreamTrack | null>(null);

  // Keep the latest onFailure reachable from the connection effect without
  // adding it to the effect deps (which would tear down and reconnect the WS).
  const onFailureRef = useRef(onFailure);
  onFailureRef.current = onFailure;

  useEffect(() => {
    const ws = api.webrtcH264();
    const videoElement = videoRef.current;

    let video: RTCPeerConnection | null = null;
    let audio: RTCPeerConnection | null = null;
    let started = false;
    let disposed = false;

    // Signaling handlers are created once the peer connections exist (i.e. once
    // the ICE servers arrive), so they're null until then.
    let handleVideoAnswer: ((data: any) => void) | null = null;
    let handleAudioAnswer: ((data: any) => void) | null = null;
    let handleVideoCandidate: ((data: any) => void) | null = null;
    let handleAudioCandidate: ((data: any) => void) | null = null;

    // --- Autodrop-to-MJPEG bookkeeping ---
    // Fire onFailure at most once when the WebRTC media can't come up so the
    // parent can swap in MJPEG. All timers are cleared on unmount.
    let failureFired = false;
    let connectWatchdog: ReturnType<typeof setTimeout> | null = null;
    let disconnectGrace: ReturnType<typeof setTimeout> | null = null;
    let iceServersFallback: ReturnType<typeof setTimeout> | null = null;
    let mediaPlaying = false;

    const fireFailure = () => {
      if (failureFired || disposed) {
        return;
      }
      failureFired = true;
      onFailureRef.current?.();
    };

    const clearDisconnectGrace = () => {
      if (disconnectGrace) {
        clearTimeout(disconnectGrace);
        disconnectGrace = null;
      }
    };

    // The <video> firing 'playing' is the definitive "media is up" signal: it
    // cancels the connect watchdog and any pending disconnect grace.
    const onMediaPlaying = () => {
      mediaPlaying = true;
      if (connectWatchdog) {
        clearTimeout(connectWatchdog);
        connectWatchdog = null;
      }
      clearDisconnectGrace();
    };
    if (videoElement) {
      videoElement.addEventListener('playing', onMediaPlaying);
    }

    const sendMsg = (event: string, data: string) => {
      if (ws.readyState !== WebSocket.OPEN) {
        return;
      }

      try {
        const message: WebRTCMessage = { event, data };
        ws.send(JSON.stringify(message));
      } catch (err) {
        console.error('Error sending event:', err);
      }
    };

    const handleVideoStatus = (data: number) => {
      switch (data) {
        case 1:
          setVideoStatus(VideoStatus.Normal);
          setIsPlaying(true);
          break;
        case -1:
          setVideoStatus(VideoStatus.NoImage);
          setIsPlaying(false);
          break;
        case -4:
          setVideoStatus(VideoStatus.InconsistentVideoMode);
          setIsPlaying(false);
          break;
        default:
          console.log('Unhandled video status:', data);
          break;
      }
    };

    // Defer creating BOTH peer connections until we know which ICE servers to
    // use. The server ships them (venue union first) in an "ice-servers"
    // message right after connect, so a remote viewer reaching this UI through
    // AllMyStuff's sites proxy gets relays it can actually reach — instead of a
    // hardcoded public STUN it can't. A fallback (see ws.onopen) starts with an
    // empty config if that message never arrives, so we still attempt.
    const startConnections = (iceServers: RTCIceServer[]) => {
      if (started || disposed) {
        return;
      }
      started = true;

      const videoPeer = new RTCPeerConnection({ iceServers });
      const audioPeer = new RTCPeerConnection({ iceServers });
      video = videoPeer;
      audio = audioPeer;

      handleVideoAnswer = createAnswerHandler(videoPeer, videoOfferSent, videoIceCandidates, 'video');
      handleAudioAnswer = createAnswerHandler(audioPeer, audioOfferSent, audioIceCandidates, 'audio');
      handleVideoCandidate = createCandidateHandler(videoPeer, videoIceCandidates, 'video');
      handleAudioCandidate = createCandidateHandler(audioPeer, audioIceCandidates, 'audio');

      videoOfferSent.current = false;
      audioOfferSent.current = false;

      // --- Init Video ---
      videoPeer.onnegotiationneeded = async () => {
        if (videoOfferSent.current || videoPeer.signalingState !== 'stable') {
          console.log('Skipping video negotiation - Waiting for answer or state unstable');
          return;
        }

        try {
          videoOfferSent.current = true;
          const offer = await videoPeer.createOffer({
            offerToReceiveVideo: true,
            offerToReceiveAudio: false
          });

          await videoPeer.setLocalDescription(offer);
          sendMsg('video-offer', JSON.stringify(videoPeer.localDescription));
        } catch (error) {
          videoOfferSent.current = false;
          console.error('Video negotiation failed:', error);
        }
      };

      videoPeer.onconnectionstatechange = () => {
        if (videoPeer.iceConnectionState === 'connected') {
          setIsLoading(false);
        }
      };

      videoPeer.oniceconnectionstatechange = () => {
        switch (videoPeer.iceConnectionState) {
          case 'connected':
          case 'completed':
            clearDisconnectGrace();
            break;
          case 'failed':
            console.warn('WebRTC ICE failed — dropping to MJPEG');
            fireFailure();
            break;
          case 'disconnected':
            // A transient blip often recovers; only drop if it persists.
            if (!disconnectGrace) {
              disconnectGrace = setTimeout(() => {
                disconnectGrace = null;
                const state = videoPeer.iceConnectionState;
                if (state === 'disconnected' || state === 'failed') {
                  console.warn('WebRTC ICE stayed disconnected — dropping to MJPEG');
                  fireFailure();
                }
              }, 5 * 1000);
            }
            break;
          default:
            break;
        }
      };

      videoPeer.ontrack = (event) => {
        if (videoRef.current && event.track.kind === 'video') {
          videoRef.current.srcObject = new MediaStream([event.track]);
        }
      };

      // --- Init Audio ---
      audioPeer.onnegotiationneeded = async () => {
        if (audioOfferSent.current || audioPeer.signalingState !== 'stable') {
          console.log('Skipping audio negotiation - Waiting for answer or state unstable');
          return;
        }

        try {
          audioOfferSent.current = true;
          const offer = await audioPeer.createOffer({
            offerToReceiveVideo: false,
            offerToReceiveAudio: true
          });

          await audioPeer.setLocalDescription(offer);
          sendMsg('audio-offer', JSON.stringify(audioPeer.localDescription));
        } catch (error) {
          audioOfferSent.current = false;
          console.error('Audio negotiation failed:', error);
        }
      };

      audioPeer.ontrack = (event) => {
        if (audioRef.current && event.track.kind === 'audio') {
          audioRef.current.srcObject = new MediaStream([event.track]);
        }
      };

      videoPeer.onicecandidate = (event) => {
        if (event.candidate) {
          sendMsg('video-candidate', JSON.stringify(event.candidate));
        }
      };

      audioPeer.onicecandidate = (event) => {
        if (event.candidate) {
          sendMsg('audio-candidate', JSON.stringify(event.candidate));
        }
      };

      videoPeer.addTransceiver('video', { direction: 'recvonly' });
      audioPeer.addTransceiver('audio', { direction: 'sendrecv' });

      audioConnectionRef.current = audioPeer;
    };

    // --- WebSocket Message Handling ---
    ws.onopen = () => {
      if (disposed) {
        ws.close();
        return;
      }

      videoOfferSent.current = false;
      audioOfferSent.current = false;

      // Fallback: if the server never sends ice-servers, still attempt with an
      // empty config so Part B's autodrop can catch a hard failure.
      iceServersFallback = setTimeout(() => {
        if (!started) {
          console.warn('No ice-servers from server — starting WebRTC with default config');
          startConnections([]);
        }
      }, 3 * 1000);

      // Connect watchdog: if <video> hasn't started playing within 10s of the
      // socket opening, drop to MJPEG. onMediaPlaying cancels this.
      connectWatchdog = setTimeout(() => {
        if (!mediaPlaying) {
          console.warn('WebRTC media did not start within 10s — dropping to MJPEG');
          fireFailure();
        }
      }, 10 * 1000);
    };

    ws.onmessage = (event) => {
      try {
        const msg = JSON.parse(event.data as string) as WebRTCMessage;
        if (!msg?.data) return;

        const data = JSON.parse(msg.data);
        if (!data) return;

        switch (msg.event) {
          case 'ice-servers':
            startConnections(Array.isArray(data) ? (data as RTCIceServer[]) : []);
            break;
          case 'video-answer':
            handleVideoAnswer?.(data);
            break;
          case 'video-candidate':
            handleVideoCandidate?.(data);
            break;
          case 'audio-answer':
            handleAudioAnswer?.(data);
            break;
          case 'audio-candidate':
            handleAudioCandidate?.(data);
            break;
          case 'video-status':
            handleVideoStatus(Number(data));
            break;
          case 'heartbeat':
            break;
          default:
            console.log('Unhandled event:', msg.event);
        }
      } catch (err) {
        console.error('Message processing error:', err);
      }
    };

    const heartbeatTimer = setInterval(() => {
      sendMsg('heartbeat', '');
    }, 60 * 1000);

    const loadingTimer = setTimeout(() => {
      setIsLoading(false);
    }, 10 * 1000);

    return () => {
      disposed = true;

      if (ws.readyState === WebSocket.OPEN) {
        ws.close();
      }

      video?.close();
      audio?.close();

      videoOfferSent.current = false;
      audioOfferSent.current = false;

      setVolume(0);

      clearInterval(heartbeatTimer);
      clearTimeout(loadingTimer);
      if (connectWatchdog) {
        clearTimeout(connectWatchdog);
      }
      if (iceServersFallback) {
        clearTimeout(iceServersFallback);
      }
      clearDisconnectGrace();
      if (videoElement) {
        videoElement.removeEventListener('playing', onMediaPlaying);
      }

      if (micStreamRef.current) {
        micStreamRef.current.getTracks().forEach((track) => track.stop());
        micStreamRef.current = null;
      }
      micSenderRef.current = null;
      micTrackRef.current = null;
    };
  }, []);

  useEffect(() => {
    const audioElement = audioRef.current;
    if (!audioElement) return;

    if (volume > 0) {
      if (audioElement.paused) {
        audioElement.play().catch(console.error);
      }
      if (audioElement.muted) {
        audioElement.muted = false;
      }
    }

    audioElement.volume = volume / 100;
  }, [volume]);

  // Handle microphone enable/disable
  useEffect(() => {
    const audio = audioConnectionRef.current;
    if (!audio) return;

    const enableMicrophone = async () => {
      try {
        const stream = await navigator.mediaDevices.getUserMedia({
          audio: {
            sampleRate: 48000,
            channelCount: 2,
            echoCancellation: true,
            noiseSuppression: true,
            autoGainControl: true
          }
        });
        micStreamRef.current = stream;

        const track = stream.getAudioTracks()[0];
        micTrackRef.current = track;

        micSenderRef.current = audio.addTrack(track, stream);
      } catch (err) {
        console.error('Failed to get microphone:', err);
        setMicEnabled(false);
      }
    };

    const disableMicrophone = () => {
      if (micSenderRef.current) {
        try {
          audio.removeTrack(micSenderRef.current);
        } catch (e) {
          console.error('Failed to remove track:', e);
        }
        micSenderRef.current = null;
      }

      micTrackRef.current = null;
      if (micStreamRef.current) {
        micStreamRef.current.getTracks().forEach((track) => track.stop());
        micStreamRef.current = null;
      }
    };

    if (micEnabled) {
      enableMicrophone();
    } else {
      disableMicrophone();
    }
  }, [micEnabled, setMicEnabled]);

  return (
    <Spin size="large" tip="Loading" spinning={isLoading}>
      <div className="flex h-screen w-screen items-start justify-center xl:items-center">
        <video
          id="screen"
          ref={videoRef}
          className={clsx(
            'block max-h-full min-h-[50vh] min-w-[50vw] max-w-full select-none object-scale-down',
            isPlaying ? 'opacity-100' : 'opacity-0',
            mouseStyle
          )}
          style={{ transform: `scale(${videoParameters.scale})` }}
          muted
          autoPlay
          playsInline
          controls={false}
          onPlaying={() => setIsLoading(false)}
          onClick={(e) => {
            e.stopPropagation();
            e.preventDefault();
          }}
        />

        <audio ref={audioRef} muted autoPlay playsInline />
      </div>
    </Spin>
  );
};
