import {readdirSync, readFileSync} from 'node:fs';
import {describe, expect, it} from 'vitest';

import {MESSAGES} from './messages.js';
import {FLAGS, RULES} from './model.js';

// every t('…') of the page has its German and English text
describe('messages', () => {
    it('covers every key', () => {
        const dir = process.cwd() + '/src/';
        const missing: string[] = [];
        for (const f of readdirSync(dir).filter((f) => f.endsWith('.svelte') || f === 'model.ts')) {
            const src = readFileSync(dir + f, 'utf8');
            for (const m of src.matchAll(/\bt\((?:'((?:[^'\\]|\\.)*)'|"((?:[^"\\]|\\.)*)")/g)) {
                const key = (m[1] ?? m[2]!).replace(/\\'/g, "'");
                if (key.endsWith('.') || key.endsWith('.' + '')) continue;
                if (/\.\s*\+/.test(key)) continue;
                const e = MESSAGES[key] as {de?: string; en?: string} | undefined;
                if (!e?.de || !e?.en) missing.push(`${f}: ${key}`);
            }
        }
        for (const k of RULES) if (!MESSAGES['rule.' + k]) missing.push('rule.' + k);
        for (const k of Object.keys(FLAGS)) if (!MESSAGES['flag.' + k]) missing.push('flag.' + k);
        for (const k of ['poweroff', 'reboot']) if (!MESSAGES['action.' + k]) missing.push('action.' + k);
        expect(missing).toEqual([]);
    });
    it('never says "box"', () => {
        for (const [k, v] of Object.entries(MESSAGES)) {
            const e = v as {de: string; en: string};
            expect(`${k} ${e.de} ${e.en}`).not.toMatch(/\bbox\b/i);
        }
    });
});
