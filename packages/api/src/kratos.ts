// Member sign-in by phone and SMS code: the Ory Kratos native (API) self-service
// flows, reached through the auth gateway (ADR-0008 addendum). The gateway is
// mounted at <kratosUrl> (for example https://api.example.invalid/auth) and
// forwards an allowlist of Kratos endpoints after its own checks.
//
// Kratos is not part of contracts/openapi; the few response fields read here
// are typed by hand and checked at runtime, everything else is ignored.
//
// Flow:
//   1. login: GET /self-service/login/api, then POST the phone with method
//      "code". Kratos sends the SMS and answers 400 with the flow in state
//      "sent_email" (Kratos uses that name for any code channel).
//   2. If no identity has this phone, Kratos answers with message 4000035;
//      we switch to the registration flow with traits.<phoneTrait>.
//   3. POST the code to the same flow: 200 with session_token.
// Every POST needs a fresh proof-of-work solution (X-Pow-Solution).
//
// Privacy: the phone number and the code are sent only in request bodies to
// the gateway. Nothing here logs; error objects carry codes, never input.
import { NetworkError } from './errors';
import { solvePow, type PowChallenge, type Sha256Hex } from './pow';

/** SignInErrorCode is what the sign-in screen shows a text for (auth:errors.<code>). */
export type SignInErrorCode =
  'invalid_phone' | 'rate_limited' | 'invalid_code' | 'flow_expired' | 'auth_unavailable' | 'network' | 'unknown';

/** SignInError is a failed sign-in step with a code for the user-facing text. */
export class SignInError extends Error {
  readonly code: SignInErrorCode;
  constructor(code: SignInErrorCode, cause?: unknown) {
    super(`sign-in failed: ${code}`, { cause });
    this.name = 'SignInError';
    this.code = code;
  }
}

/** PendingSignIn is the state between "code sent" and "code entered". Keep it in memory only. */
export interface PendingSignIn {
  flow: 'login' | 'registration';
  flowId: string;
  phone: string;
}

/** Session is the result of a successful sign-in. */
export interface Session {
  token: string;
  /** expiresAt is an ISO 8601 time, when Kratos returned one. */
  expiresAt?: string;
}

/** KratosConfig configures createKratosClient. */
export interface KratosConfig {
  /** baseUrl is the auth gateway root, e.g. "https://api.example.invalid/auth". */
  baseUrl: string;
  sha256Hex: Sha256Hex;
  /** phoneTrait is the identity schema trait holding the phone. Default "phone" (as the gateway). */
  phoneTrait?: string;
  fetch?: typeof fetch;
}

/** KratosClient is the sign-in API used by the app. */
export interface KratosClient {
  /** startSignIn asks for an SMS code for an E.164 phone number. */
  startSignIn(phone: string): Promise<PendingSignIn>;
  /** verifyCode completes the flow and returns the session. */
  verifyCode(pending: PendingSignIn, code: string): Promise<Session>;
  /** signOut revokes the session token on the server. Best effort. */
  signOut(token: string): Promise<void>;
}

// Kratos UI message ids (https://www.ory.sh/docs/kratos/concepts/ui-messages).
const MSG_ACCOUNT_MISSING = 4000035;
const MSG_CODE_SENT = new Set([1010014, 1040005]);
const MSG_CODE_INVALID = new Set([4010008, 4040003]);

interface UiMessage {
  id?: number;
  type?: string;
}
interface FlowBody {
  id?: string;
  state?: string;
  ui?: { messages?: UiMessage[]; nodes?: { messages?: UiMessage[] }[] };
  session_token?: string;
  session?: { expires_at?: string };
  error?: { id?: string };
  code?: string;
}

/** E164 matches what the gateway accepts: "+" and 8 to 15 digits, no leading zero. */
export const E164 = /^\+[1-9][0-9]{7,14}$/;

function messages(body: FlowBody): UiMessage[] {
  const top = body.ui?.messages ?? [];
  const nodes = (body.ui?.nodes ?? []).flatMap((n) => n.messages ?? []);
  return [...top, ...nodes];
}

/** createKratosClient builds the sign-in client. */
export function createKratosClient(config: KratosConfig): KratosClient {
  const base = config.baseUrl.replace(/\/+$/, '');
  const doFetch = config.fetch ?? fetch;
  const phoneTrait = config.phoneTrait ?? 'phone';

  async function request(path: string, init: RequestInit): Promise<{ status: number; body: FlowBody }> {
    let res: Response;
    try {
      res = await doFetch(base + path, {
        ...init,
        headers: { Accept: 'application/json', ...(init.headers as Record<string, string> | undefined) },
      });
    } catch (cause) {
      throw new SignInError('network', new NetworkError(cause));
    }
    let body: FlowBody = {};
    try {
      body = (await res.json()) as FlowBody;
    } catch {
      // Empty or non-JSON body (e.g. 204 on logout): status alone decides.
    }
    return { status: res.status, body };
  }

  // Maps gateway problems (problem+json with code) and Kratos statuses.
  function failure(status: number, body: FlowBody): SignInError {
    if (typeof body.code === 'string') {
      switch (body.code) {
        case 'invalid_phone':
        case 'rate_limited':
        case 'auth_unavailable':
          return new SignInError(body.code);
        case 'pow_required':
          // A fresh solution is made for every POST, so this means the solution
          // expired in flight or was replayed; restarting the step fixes it.
          return new SignInError('flow_expired');
      }
    }
    if (status === 410 || body.error?.id === 'self_service_flow_expired') return new SignInError('flow_expired');
    if (status === 429) return new SignInError('rate_limited');
    if (status >= 500) return new SignInError('auth_unavailable');
    return new SignInError('unknown');
  }

  async function newFlow(kind: 'login' | 'registration'): Promise<string> {
    const { status, body } = await request(`/self-service/${kind}/api`, { method: 'GET' });
    if (status !== 200 || typeof body.id !== 'string') throw failure(status, body);
    return body.id;
  }

  async function submit(kind: 'login' | 'registration', flowId: string, payload: object) {
    const challenge = await request('/pow', { method: 'GET' });
    if (challenge.status !== 200) throw failure(challenge.status, challenge.body);
    const solution = await solvePow(challenge.body as unknown as PowChallenge, config.sha256Hex);
    return request(`/self-service/${kind}?flow=${encodeURIComponent(flowId)}`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json', 'X-Pow-Solution': solution },
      body: JSON.stringify(payload),
    });
  }

  function codeSent(status: number, body: FlowBody): boolean {
    return status === 400 && (body.state === 'sent_email' || messages(body).some((m) => MSG_CODE_SENT.has(m.id ?? 0)));
  }

  return {
    async startSignIn(phone) {
      if (!E164.test(phone)) throw new SignInError('invalid_phone');
      const loginId = await newFlow('login');
      const login = await submit('login', loginId, { method: 'code', identifier: phone });
      if (codeSent(login.status, login.body)) return { flow: 'login', flowId: loginId, phone };
      if (!messages(login.body).some((m) => m.id === MSG_ACCOUNT_MISSING)) throw failure(login.status, login.body);

      const regId = await newFlow('registration');
      const reg = await submit('registration', regId, { method: 'code', traits: { [phoneTrait]: phone } });
      if (codeSent(reg.status, reg.body)) return { flow: 'registration', flowId: regId, phone };
      throw failure(reg.status, reg.body);
    },

    async verifyCode(pending, code) {
      const trimmed = code.trim();
      if (!/^[0-9]{4,12}$/.test(trimmed)) throw new SignInError('invalid_code');
      const payload =
        pending.flow === 'login'
          ? { method: 'code', identifier: pending.phone, code: trimmed }
          : { method: 'code', traits: { [phoneTrait]: pending.phone }, code: trimmed };
      const { status, body } = await submit(pending.flow, pending.flowId, payload);
      if (status === 200 && typeof body.session_token === 'string' && body.session_token !== '') {
        const expiresAt = body.session?.expires_at;
        return expiresAt ? { token: body.session_token, expiresAt } : { token: body.session_token };
      }
      if (status === 400 && messages(body).some((m) => MSG_CODE_INVALID.has(m.id ?? 0) || m.type === 'error')) {
        throw new SignInError('invalid_code');
      }
      throw failure(status, body);
    },

    async signOut(token) {
      try {
        await request('/self-service/logout/api', {
          method: 'DELETE',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ session_token: token }),
        });
      } catch {
        // The local token is deleted regardless; an unrevoked server session
        // expires on its own.
      }
    },
  };
}
