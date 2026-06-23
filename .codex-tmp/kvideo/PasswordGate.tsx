'use client';

import { useEffect, useRef, useState } from 'react';
import { Eye, EyeOff, User } from 'lucide-react';
import { clearSession, getSession, setSession, type AuthSession } from '@/lib/store/auth-store';
import { useSubscriptionSync } from '@/lib/hooks/useSubscriptionSync';
import { hasStoredAppSetting, settingsStore } from '@/lib/store/settings-store';
import { useIPTVStore } from '@/lib/store/iptv-store';

type LoginMode = 'none' | 'legacy_password' | 'managed';

function syncIPTVSources(rawValue: string) {
  const iptvStore = useIPTVStore.getState();

  let entries: { name: string; url: string }[] = [];

  try {
    const parsed = JSON.parse(rawValue);
    if (Array.isArray(parsed)) {
      entries = parsed.filter((item: unknown): item is { name: string; url: string } => {
        if (!item || typeof item !== 'object') return false;
        const candidate = item as { name?: unknown; url?: unknown };
        return typeof candidate.url === 'string';
      });
    }
  } catch {
    if (rawValue.includes('http')) {
      const urls = rawValue.split(',').map((value) => value.trim()).filter((value) => value.startsWith('http'));
      entries = urls.map((url, index) => ({
        name: urls.length > 1 ? `直播源 ${index + 1}` : '直播源',
        url,
      }));
    }
  }

  iptvStore.syncBuiltinSources(entries);
}

function syncMergeSources(rawValue: string) {
  const enabled = rawValue === 'true' || rawValue === '1';
  if (!enabled) return;

  const settings = settingsStore.getSettings();
  if (settings.searchDisplayMode !== 'grouped') {
    settingsStore.saveSettings({
      ...settings,
      searchDisplayMode: 'grouped',
    });
  }
}

function syncDanmakuApiUrl(rawValue: string) {
  if (!rawValue || hasStoredAppSetting('danmakuApiUrl')) return;

  const settings = settingsStore.getSettings();
  if (settings.danmakuApiUrl !== rawValue) {
    settingsStore.saveSettings({
      ...settings,
      danmakuApiUrl: rawValue,
    });
  }
}

function applyRuntimeConfig(data: {
  subscriptionSources?: string;
  iptvSources?: string;
  mergeSources?: string;
  danmakuApiUrl?: string;
}) {
  if (data.subscriptionSources) {
    settingsStore.syncEnvSubscriptions(data.subscriptionSources);
  }

  if (data.iptvSources) {
    syncIPTVSources(data.iptvSources);
  }

  if (data.mergeSources) {
    syncMergeSources(data.mergeSources);
  }

  if (data.danmakuApiUrl) {
    syncDanmakuApiUrl(data.danmakuApiUrl);
  }
}

function toAuthSession(session: {
  accountId: string;
  profileId: string;
  username?: string;
  name: string;
  role: AuthSession['role'];
  customPermissions?: AuthSession['customPermissions'];
  mode?: AuthSession['mode'];
}): AuthSession {
  return {
    accountId: session.accountId,
    profileId: session.profileId,
    username: session.username,
    name: session.name,
    role: session.role,
    customPermissions: session.customPermissions,
    mode: session.mode,
  };
}

function ParticleCanvas() {
  const canvasRef = useRef<HTMLCanvasElement | null>(null);

  useEffect(() => {
    const canvas = canvasRef.current;
    const ctx = canvas?.getContext('2d');
    if (!canvas || !ctx) return;

    let animationId = 0;
    const particles = Array.from({ length: Math.min(90, Math.floor(window.innerWidth / 18)) }, () => ({
      x: Math.random() * window.innerWidth,
      y: Math.random() * window.innerHeight,
      vx: (Math.random() - 0.5) * 0.45,
      vy: (Math.random() - 0.5) * 0.45,
      radius: Math.random() * 1.4 + 0.5,
      opacity: Math.random() * 0.55 + 0.18,
    }));

    const resize = () => {
      const dpr = window.devicePixelRatio || 1;
      canvas.width = window.innerWidth * dpr;
      canvas.height = window.innerHeight * dpr;
      canvas.style.width = `${window.innerWidth}px`;
      canvas.style.height = `${window.innerHeight}px`;
      ctx.setTransform(dpr, 0, 0, dpr, 0, 0);
    };

    const animate = () => {
      const width = window.innerWidth;
      const height = window.innerHeight;
      ctx.clearRect(0, 0, width, height);

      for (const particle of particles) {
        particle.x += particle.vx;
        particle.y += particle.vy;
        if (particle.x < 0 || particle.x > width) particle.vx *= -1;
        if (particle.y < 0 || particle.y > height) particle.vy *= -1;
      }

      for (let i = 0; i < particles.length; i += 1) {
        for (let j = i + 1; j < particles.length; j += 1) {
          const dx = particles[i].x - particles[j].x;
          const dy = particles[i].y - particles[j].y;
          const distance = Math.sqrt(dx * dx + dy * dy);

          if (distance < 150) {
            const alpha = (1 - distance / 150) * 0.16;
            ctx.beginPath();
            ctx.strokeStyle = `rgba(255,255,255,${alpha})`;
            ctx.lineWidth = 0.5;
            ctx.moveTo(particles[i].x, particles[i].y);
            ctx.lineTo(particles[j].x, particles[j].y);
            ctx.stroke();
          }
        }
      }

      for (const particle of particles) {
        ctx.beginPath();
        ctx.arc(particle.x, particle.y, particle.radius, 0, Math.PI * 2);
        ctx.fillStyle = `rgba(255,255,255,${particle.opacity})`;
        ctx.fill();
      }

      animationId = requestAnimationFrame(animate);
    };

    resize();
    animate();
    window.addEventListener('resize', resize);

    return () => {
      window.removeEventListener('resize', resize);
      cancelAnimationFrame(animationId);
    };
  }, []);

  return <canvas ref={canvasRef} className="absolute inset-0 z-0 h-full w-full pointer-events-none" />;
}

function AuthEye({ small = false, hidden = false }: { small?: boolean; hidden?: boolean }) {
  const size = small ? 'h-4 w-4' : 'h-5 w-5';
  const pupil = small ? 'h-1.5 w-1.5' : 'h-2 w-2';

  return (
    <span className={`${size} flex items-center justify-center rounded-full bg-white transition-all duration-300 ${hidden ? 'scale-y-[0.18]' : ''}`}>
      <span className={`${pupil} rounded-full bg-[#2D2D2D] transition-transform duration-300 ${hidden ? 'translate-x-0' : 'translate-x-1'}`} />
    </span>
  );
}

function AuthCharacters({
  isTyping,
  passwordLength,
  showPassword,
}: {
  isTyping: boolean;
  passwordLength: number;
  showPassword: boolean;
}) {
  const guarded = passwordLength > 0 && !showPassword;
  const peeking = passwordLength > 0 && showPassword;

  return (
    <div className="relative h-[400px] w-[550px]">
      <div
        className="absolute bottom-0 left-[70px] z-10 w-[180px] origin-bottom rounded-t-[10px] bg-[#6C3FF5] transition-all duration-700 ease-in-out"
        style={{
          height: guarded || isTyping ? 440 : 400,
          transform: peeking
            ? 'skewX(0deg)'
            : guarded || isTyping
              ? 'skewX(-12deg) translateX(40px)'
              : 'skewX(-4deg)',
        }}
      >
        <div className={`absolute flex gap-8 transition-all duration-700 ${peeking ? 'left-5 top-9' : isTyping ? 'left-14 top-16' : 'left-12 top-10'}`}>
          <AuthEye hidden={guarded} />
          <AuthEye hidden={guarded} />
        </div>
      </div>

      <div
        className="absolute bottom-0 left-[240px] z-20 h-[310px] w-[120px] origin-bottom rounded-t-lg bg-[#2D2D2D] transition-all duration-700 ease-in-out"
        style={{ transform: isTyping ? 'skewX(8deg) translateX(16px)' : 'skewX(0deg)' }}
      >
        <div className={`absolute flex gap-6 transition-all duration-700 ${peeking ? 'left-3 top-8' : isTyping ? 'left-8 top-4' : 'left-7 top-8'}`}>
          <AuthEye small hidden={guarded} />
          <AuthEye small hidden={guarded} />
        </div>
      </div>

      <div className="absolute bottom-0 left-0 z-30 h-[200px] w-[240px] origin-bottom rounded-t-full bg-[#FF9B6B] transition-transform duration-700 ease-in-out">
        <div className={`absolute flex gap-8 transition-all duration-300 ${peeking ? 'left-[50px] top-[85px]' : 'left-[82px] top-[90px]'}`}>
          <span className="h-3 w-3 rounded-full bg-[#2D2D2D]" />
          <span className="h-3 w-3 rounded-full bg-[#2D2D2D]" />
        </div>
      </div>

      <div className="absolute bottom-0 left-[310px] z-40 h-[230px] w-[140px] origin-bottom rounded-t-full bg-[#E8D754] transition-transform duration-700 ease-in-out">
        <div className={`absolute flex gap-6 transition-all duration-300 ${peeking ? 'left-5 top-9' : 'left-[52px] top-10'}`}>
          <span className="h-3 w-3 rounded-full bg-[#2D2D2D]" />
          <span className="h-3 w-3 rounded-full bg-[#2D2D2D]" />
        </div>
        <div className={`absolute h-1 w-20 rounded-full bg-[#2D2D2D] transition-all duration-300 ${peeking ? 'left-3 top-[88px]' : 'left-10 top-[88px]'}`} />
      </div>
    </div>
  );
}

export function PasswordGate({
  children,
  hasAuth: initialHasAuth,
}: {
  children: React.ReactNode;
  hasAuth: boolean;
}) {
  useSubscriptionSync();

  const [isLocked, setIsLocked] = useState(true);
  const [username, setUsername] = useState('');
  const [password, setPassword] = useState('');
  const [error, setError] = useState('');
  const [isClient, setIsClient] = useState(false);
  const [persistSession, setPersistSession] = useState(true);
  const [isValidating, setIsValidating] = useState(false);
  const [loginMode, setLoginMode] = useState<LoginMode>('none');
  const [showPassword, setShowPassword] = useState(false);
  const [isTyping, setIsTyping] = useState(false);

  useEffect(() => {
    let mounted = true;

    const init = async () => {
      const mirroredSession = getSession();

      try {
        const [configRes, sessionRes] = await Promise.all([
          fetch('/api/auth'),
          fetch('/api/auth/session'),
        ]);

        if (!configRes.ok) {
          throw new Error('Failed to fetch auth config');
        }

        const config = await configRes.json();
        const sessionStatus = sessionRes.ok ? await sessionRes.json() : { authenticated: false, session: null };

        if (!mounted) return;

        setPersistSession(config.persistSession);
        setLoginMode(config.loginMode || 'none');
        applyRuntimeConfig(config);

        if (sessionStatus.authenticated && sessionStatus.session) {
          const session = toAuthSession(sessionStatus.session);
          const hasMatchingMirror = mirroredSession &&
            mirroredSession.accountId === session.accountId &&
            mirroredSession.profileId === session.profileId;

          setSession(session, config.persistSession);

          if (!hasMatchingMirror) {
            window.location.reload();
            return;
          }

          setIsLocked(false);
          setIsClient(true);
          return;
        }

        if (mirroredSession) {
          clearSession();
          window.location.reload();
          return;
        }

        setIsLocked(!!config.hasAuth);
        setIsClient(true);
      } catch {
        if (!mounted) return;
        setIsLocked(initialHasAuth && !mirroredSession);
        setIsClient(true);
      }
    };

    init();

    return () => {
      mounted = false;
    };
  }, [initialHasAuth]);

  const handleUnlock = async (event: React.FormEvent) => {
    event.preventDefault();
    setIsValidating(true);
    setError('');

    try {
      const response = await fetch('/api/auth', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({
          username: loginMode === 'managed' ? username : undefined,
          password,
        }),
      });
      const data = await response.json();

      if (data.valid && data.session) {
        setSession(toAuthSession(data.session), data.persistSession ?? persistSession);
        window.location.reload();
        return;
      }
    } catch {
      // Ignore network errors and show the same message as invalid credentials.
    }

    setError(loginMode === 'managed' ? '用户名或密码错误' : '密码错误');
    setIsValidating(false);
    const form = document.getElementById('password-form');
    form?.classList.add('animate-shake');
    setTimeout(() => form?.classList.remove('animate-shake'), 500);
  };

  if (!isClient) return null;

  if (!isLocked) {
    return <>{children}</>;
  }

  const showManagedFields = loginMode === 'managed';

  return (
    <div className="fixed inset-0 z-[9999] grid min-h-screen overflow-hidden bg-[#050B14] text-white lg:grid-cols-2">
      <ParticleCanvas />

      <div className="relative z-10 hidden flex-col justify-between p-12 text-white lg:flex">
        <div className="relative z-20 text-lg font-semibold tracking-tight">无风影视</div>
        <div className="relative z-20 flex h-[500px] items-end justify-center">
          <AuthCharacters
            isTyping={isTyping}
            passwordLength={password.length}
            showPassword={showPassword}
          />
        </div>
      </div>

      <div className="relative z-10 flex w-full items-center justify-center p-6 sm:p-8">
        <div className="w-full max-w-[420px]">
        <form
          id="password-form"
          onSubmit={handleUnlock}
          className="flex flex-col gap-5 rounded-3xl border border-white/10 bg-white/5 p-8 shadow-2xl backdrop-blur-xl transition-all duration-300"
        >
          <div className="mb-5 text-center">
            <h1 className="mb-2 text-3xl font-bold tracking-tight text-white">欢迎回来！</h1>
            <p className="text-sm text-gray-400">
              {showManagedFields ? '请输入您的账户信息' : '请输入访问密码继续观影'}
            </p>
          </div>

          <div className="w-full space-y-4">
            {showManagedFields && (
              <div>
                <label className="mb-2 block text-sm font-medium text-gray-200">用户名</label>
                <div className="relative">
                  <User size={16} className="absolute left-4 top-1/2 -translate-y-1/2 text-gray-500" />
                  <input
                    type="text"
                    value={username}
                    onChange={(event) => {
                      setUsername(event.target.value);
                      setError('');
                    }}
                    onFocus={() => setIsTyping(true)}
                    onBlur={() => setIsTyping(false)}
                    placeholder="请输入用户名"
                    className="h-12 w-full rounded-xl border border-white/10 bg-black/40 pl-11 pr-4 text-white placeholder:text-gray-500 transition-colors focus:border-white focus:outline-none"
                    autoComplete="username"
                    autoFocus
                  />
                </div>
              </div>
            )}

            <div>
              <label className="mb-2 block text-sm font-medium text-gray-200">密码</label>
              <div className="relative">
                <input
                  type={showPassword ? 'text' : 'password'}
                  value={password}
                  onChange={(event) => {
                    setPassword(event.target.value);
                    setError('');
                  }}
                  onFocus={() => setIsTyping(true)}
                  onBlur={() => setIsTyping(false)}
                  placeholder="请输入密码"
                  className={`h-12 w-full rounded-xl border bg-black/40 pl-4 pr-11 text-white placeholder:text-gray-500 transition-colors focus:border-white focus:outline-none ${error ? 'border-red-500' : 'border-white/10'}`}
                  autoFocus={!showManagedFields}
                  autoComplete={showManagedFields ? 'current-password' : 'off'}
                />
                <button
                  type="button"
                  onClick={() => setShowPassword((value) => !value)}
                  className="absolute inset-y-0 right-0 flex items-center pr-4 text-gray-500 transition-colors hover:text-gray-300"
                  aria-label={showPassword ? '隐藏密码' : '显示密码'}
                >
                  {showPassword ? <EyeOff size={20} /> : <Eye size={20} />}
                </button>
              </div>
              {error && (
                <p className="mt-2 text-center text-sm text-red-400 animate-pulse">
                  {error}
                </p>
              )}
            </div>

            <button
              type="submit"
              disabled={isValidating}
              className="mt-2 flex h-12 w-full items-center justify-center rounded-xl bg-white px-4 font-semibold text-black transition-colors hover:bg-gray-200 disabled:cursor-not-allowed disabled:opacity-50"
            >
              {isValidating ? '验证中...' : '登录'}
            </button>
          </div>
        </form>
        </div>
      </div>
      <style jsx global>{`
        @keyframes shake {
          0%, 100% { transform: translateX(0); }
          25% { transform: translateX(-5px); }
          75% { transform: translateX(5px); }
        }
        .animate-shake {
          animation: shake 0.3s cubic-bezier(.36,.07,.19,.97) both;
        }
      `}</style>
    </div>
  );
}
