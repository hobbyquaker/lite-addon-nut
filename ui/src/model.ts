// The API's shapes and the page's pure helpers (tested in model.test.ts).

export interface Server {
    id: string;
    name: string;
    host: string;
    port: number;
    username: string;
    password?: string;
    tls: 'off' | 'starttls';
    tls_verify: boolean;
    ca_cert?: string;
}

export interface Rules {
    low_battery: boolean;
    fsd: boolean;
    charge_below: number;
    runtime_below: number;
    on_battery_longer: number;
    comm_lost: boolean;
}

export interface UPS {
    id: string;
    server: string;
    name: string;
    label: string;
    enabled: boolean;
    rules: Rules;
}

export interface Settings {
    enabled: boolean;
    action: 'poweroff' | 'reboot';
    dry_run: boolean;
    grace_s: number;
    poll_s: number;
    dead_s: number;
    require_all: boolean;
}

export interface Right {
    checked: boolean;
    ok: boolean;
    scopes: string[];
    error?: string;
    user?: string;
}

export interface Config {
    servers: Server[];
    ups: UPS[];
    settings: Settings;
    defaults: {settings: Settings; rules: Rules};
    right: Right;
    version: string;
}

export interface Reason {
    rule: string;
    value: number;
    threshold: number;
}

export interface UPSState {
    id: string;
    label: string;
    server: string;
    server_name: string;
    name: string;
    enabled: boolean;
    connected: boolean;
    error?: string;
    last_ok?: number;
    data_age_s?: number;
    status: string[];
    on_battery: boolean;
    on_battery_s: number;
    charge?: number;
    runtime?: number;
    load?: number;
    input_voltage?: number;
    output_voltage?: number;
    battery_voltage?: number;
    temperature?: number;
    model?: string;
    mfr?: string;
    serial?: string;
    vars?: Record<string, string>;
    reasons: Reason[];
    suppressed: boolean;
    rules: Rules;
}

export interface Countdown {
    since: number;
    until: number;
    reasons: string[];
    dry_run: boolean;
    action: string;
}

export interface ShutdownRecord {
    at: number;
    action: string;
    dry_run: boolean;
    test: boolean;
    reasons: string[];
    error?: string;
}

export interface State {
    enabled: boolean;
    dry_run: boolean;
    action: string;
    require_all: boolean;
    grace_s: number;
    ups: UPSState[];
    countdown?: Countdown;
    shutdown?: ShutdownRecord;
    issued: boolean;
    polls: number;
    last_poll?: number;
    ready: boolean;
}

export interface Status {
    state: State;
    right: Right;
    now: number;
}

export interface Event {
    at: string;
    ups?: string;
    label?: string;
    kind: string;
    status?: string;
    prev?: string;
    charge?: number;
    runtime?: number;
    reasons?: string[];
    grace_s?: number;
    dry_run?: boolean;
    action?: string;
    test?: boolean;
    message?: string;
}

export interface ProbeResult {
    version: string;
    tls: boolean;
    ups: {name: string; description: string}[];
}

export const RULES = ['low_battery', 'fsd', 'charge_below', 'runtime_below', 'on_battery_longer', 'comm_lost'] as const;

/** The flags upsd reports and how they colour: good, warn, bad, or neutral. */
export const FLAGS: Record<string, 'ok' | 'warn' | 'bad' | 'neutral'> = {
    OL: 'ok',
    OB: 'warn',
    LB: 'bad',
    HB: 'neutral',
    RB: 'bad',
    CHRG: 'neutral',
    DISCHRG: 'warn',
    BYPASS: 'warn',
    CAL: 'neutral',
    OFF: 'bad',
    OVER: 'bad',
    TRIM: 'neutral',
    BOOST: 'neutral',
    FSD: 'bad',
    ALARM: 'bad',
};

export function flagClass(flag: string): string {
    return FLAGS[flag] ?? 'neutral';
}

/** Seconds as h:mm:ss or m:ss. */
export function formatSeconds(s: number | undefined): string {
    if (s === undefined || s === null || !Number.isFinite(s) || s < 0) return '–';
    const total = Math.round(s);
    const h = Math.floor(total / 3600);
    const m = Math.floor((total % 3600) / 60);
    const sec = total % 60;
    const mm = h > 0 ? String(m).padStart(2, '0') : String(m);
    return `${h > 0 ? h + ':' : ''}${mm}:${String(sec).padStart(2, '0')}`;
}

/** A number with a unit, or a dash. */
export function withUnit(v: number | undefined, unit: string, digits = 0): string {
    if (v === undefined || v === null || !Number.isFinite(v)) return '–';
    return `${v.toFixed(digits)}${unit ? ' ' + unit : ''}`;
}

/** The one-line summary of a UPS's rules for the settings table. */
export function rulesSummary(r: Rules, t: (k: string, p?: Record<string, string | number>) => string): string {
    const parts: string[] = [];
    if (r.low_battery) parts.push(t('rule.low_battery'));
    if (r.fsd) parts.push(t('rule.fsd'));
    if (r.charge_below > 0) parts.push(t('charge < {n} %', {n: r.charge_below}));
    if (r.runtime_below > 0) parts.push(t('runtime < {n} s', {n: r.runtime_below}));
    if (r.on_battery_longer > 0) parts.push(t('on battery > {n} s', {n: r.on_battery_longer}));
    if (r.comm_lost) parts.push(t('rule.comm_lost'));
    return parts.length ? parts.join(' · ') : t('no rule');
}

/** A reason as the status card shows it. */
export function reasonText(r: Reason, t: (k: string, p?: Record<string, string | number>) => string): string {
    switch (r.rule) {
        case 'charge_below':
            return t('charge {v} % below {n} %', {v: Math.round(r.value), n: r.threshold});
        case 'runtime_below':
            return t('runtime {v} below {n}', {v: formatSeconds(r.value), n: formatSeconds(r.threshold)});
        case 'on_battery_longer':
            return t('on battery for {v}, longer than {n}', {v: formatSeconds(r.value), n: formatSeconds(r.threshold)});
        case 'comm_lost':
            return t('no data for {v} while on battery', {v: formatSeconds(r.value)});
        default:
            return t('rule.' + r.rule);
    }
}

/** The sentence of an event, in the user's language. */
export function eventText(e: Event, t: (k: string, p?: Record<string, string | number>) => string): string {
    const action = e.action ? t('action.' + e.action) : '';
    const reasons = (e.reasons ?? []).join(', ');
    switch (e.kind) {
        case 'connected':
            return t('UPS found: status {status}', {status: e.status ?? ''});
        case 'comm_lost':
            return t('No data from the server: {message}', {message: e.message ?? ''});
        case 'comm_back':
            return t('The server answers again');
        case 'status':
            return t('Status {prev} → {status}', {prev: e.prev ?? '', status: e.status ?? ''}) + (e.charge !== undefined ? ` · ${Math.round(e.charge)} %` : '') + (e.runtime !== undefined ? ` · ${formatSeconds(e.runtime)}` : '');
        case 'countdown':
            return (e.dry_run ? t('Dry run: ') : '') + (e.grace_s ? t('{action} in {n} s ({reasons})', {action, n: e.grace_s, reasons}) : t('{action} now ({reasons})', {action, reasons}));
        case 'cancelled':
            return e.message === 'cancelled by the user' ? t('Countdown cancelled by the user') : t('Countdown cancelled: the condition cleared');
        case 'shutdown':
            return e.test ? t('Test: {action} ordered', {action}) : t('{action} ordered ({reasons})', {action, reasons});
        case 'dry_run':
            return e.test ? t('Test (dry run): would {action}', {action}) : t('Dry run: would {action} ({reasons})', {action, reasons});
        case 'error':
            return t('{action} failed: {message}', {action, message: e.message ?? ''});
        default:
            return e.kind + (e.message ? ': ' + e.message : '');
    }
}

export function emptyServer(): Server {
    return {id: '', name: '', host: '', port: 3493, username: '', password: '', tls: 'off', tls_verify: true, ca_cert: ''};
}

export function emptyUPS(server: string, rules: Rules): UPS {
    return {id: '', server, name: '', label: '', enabled: true, rules: {...rules}};
}
