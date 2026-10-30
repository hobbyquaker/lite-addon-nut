import {describe, expect, it} from 'vitest';

import {eventText, flagClass, formatSeconds, reasonText, rulesSummary, withUnit, type Rules} from './model.js';

const t = (k: string, p?: Record<string, string | number>) => {
    let s = k;
    for (const [n, v] of Object.entries(p ?? {})) s = s.replace(`{${n}}`, String(v));
    return s;
};

describe('formatting', () => {
    it('formats seconds', () => {
        expect(formatSeconds(0)).toBe('0:00');
        expect(formatSeconds(65)).toBe('1:05');
        expect(formatSeconds(3725)).toBe('1:02:05');
        expect(formatSeconds(undefined)).toBe('–');
        expect(formatSeconds(-1)).toBe('–');
    });
    it('formats units', () => {
        expect(withUnit(23, '%')).toBe('23 %');
        expect(withUnit(27.14, 'V', 1)).toBe('27.1 V');
        expect(withUnit(undefined, 'V')).toBe('–');
    });
    it('colours flags', () => {
        expect(flagClass('OL')).toBe('ok');
        expect(flagClass('OB')).toBe('warn');
        expect(flagClass('LB')).toBe('bad');
        expect(flagClass('XYZ')).toBe('neutral');
    });
});

describe('texts', () => {
    const rules: Rules = {low_battery: true, fsd: false, charge_below: 20, runtime_below: 0, on_battery_longer: 600, comm_lost: true};
    it('summarises rules', () => {
        expect(rulesSummary(rules, t)).toBe('rule.low_battery · charge < 20 % · on battery > 600 s · rule.comm_lost');
        expect(rulesSummary({...rules, low_battery: false, charge_below: 0, on_battery_longer: 0, comm_lost: false}, t)).toBe('no rule');
    });
    it('describes reasons', () => {
        expect(reasonText({rule: 'charge_below', value: 12.4, threshold: 20}, t)).toBe('charge 12 % below 20 %');
        expect(reasonText({rule: 'runtime_below', value: 90, threshold: 300}, t)).toBe('runtime 1:30 below 5:00');
        expect(reasonText({rule: 'low_battery', value: 0, threshold: 0}, t)).toBe('rule.low_battery');
    });
    it('describes events', () => {
        expect(eventText({at: '', kind: 'status', prev: 'OL', status: 'OB DISCHRG', charge: 99.6}, t)).toBe('Status OL → OB DISCHRG · 100 %');
        expect(eventText({at: '', kind: 'countdown', action: 'poweroff', grace_s: 30, reasons: ['Eaton: low_battery'], dry_run: true}, t)).toBe('Dry run: action.poweroff in 30 s (Eaton: low_battery)');
        expect(eventText({at: '', kind: 'countdown', action: 'poweroff', grace_s: 0, reasons: ['Eaton: fsd']}, t)).toBe('action.poweroff now (Eaton: fsd)');
        expect(eventText({at: '', kind: 'shutdown', action: 'reboot', test: true}, t)).toBe('Test: action.reboot ordered');
        expect(eventText({at: '', kind: 'cancelled', message: 'cancelled by the user'}, t)).toBe('Countdown cancelled by the user');
        expect(eventText({at: '', kind: 'error', action: 'poweroff', message: '403'}, t)).toBe('action.poweroff failed: 403');
    });
});
