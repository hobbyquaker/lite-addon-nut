<script lang="ts">
    // What the UPS report and what the watch would do: the watch's summary, one card per UPS.
    import {t} from 'occulite-ui/i18n';

    import {get} from './api.js';
    import {flagClass, formatSeconds, reasonText, withUnit, type Config, type Status, type UPSState} from './model.js';

    let {config, status, onrecheck}: {config: Config; status: Status | undefined; onrecheck: () => Promise<void>} = $props();
    let open = $state<Record<string, boolean>>({});
    let rechecking = $state(false);

    async function recheck() {
        rechecking = true;
        try {
            await get('api/v1/status?recheck=1');
            await onrecheck();
        } finally {
            rechecking = false;
        }
    }

    function chargeClass(u: UPSState): string {
        if (u.charge === undefined) return '';
        if (u.charge < 20 || u.status.includes('LB')) return 'bad';
        if (u.charge < 50) return 'warn';
        return 'ok';
    }

    function variables(u: UPSState): [string, string][] {
        return Object.entries(u.vars ?? {}).sort(([a], [b]) => a.localeCompare(b));
    }

    const right = $derived(status?.right ?? config.right);
    const st = $derived(status?.state);
</script>

<section class="nut-section" data-testid="watch">
    <h2>{t('Watch')}</h2>
    <div class="nut-kv">
        <span>{t('Watch')}</span>
        <span>
            <span class="nut-pill" class:ok={config.settings.enabled && !config.settings.dry_run} class:warn={config.settings.enabled && config.settings.dry_run} class:bad={!config.settings.enabled} data-testid="watch-state">{config.settings.enabled ? (config.settings.dry_run ? t('dry run') : t('active')) : t('inactive')}</span>
        </span>
        <span>{t('Action')}</span>
        <span>{t('action.poweroff')} · {t('Grace period')}: {config.settings.grace_s > 0 ? t('{n} s', {n: config.settings.grace_s}) : t('none')}</span>
        <span>{t('Shutdown right')}</span>
        <span class="nut-row">
            <span class="nut-pill" class:ok={right.checked && right.ok} class:bad={right.checked && !right.ok} data-testid="right">{right.checked ? (right.ok ? t('granted') : t('missing')) : t('not checked yet')}</span>
            <button class="hmm-button" onclick={recheck} disabled={rechecking}>{t('Check again')}</button>
        </span>
        {#if right.checked && !right.ok}
            <p class="nut-warning nut-span">{right.error ? right.error : t("The addon's token holds {scopes}. It needs power:halt to start the shutdown; reinstall the addon or check its policy on the Addons page.", {scopes: right.scopes.join(', ') || '–'})}</p>
        {/if}
        {#if st?.shutdown}
            <span>{t('Last shutdown')}</span>
            <span data-testid="last-shutdown">
                {new Date(st.shutdown.at).toLocaleString()} · {t('action.' + st.shutdown.action)}{st.shutdown.dry_run ? ' · ' + t('dry run') : ''}{st.shutdown.test ? ' · Test' : ''}
                {#if st.shutdown.error}<span class="nut-error-inline">· {st.shutdown.error}</span>{/if}
                {#if st.shutdown.reasons.length && st.shutdown.reasons[0] !== 'test'}<span class="nut-muted">· {st.shutdown.reasons.join(', ')}</span>{/if}
            </span>
        {/if}
        {#if st?.last_poll}
            <span>{t('Last poll')}</span>
            <span class="nut-muted">{new Date(st.last_poll).toLocaleTimeString()}</span>
        {/if}
    </div>
</section>

{#if !st || st.ups.length === 0}
    <p class="nut-notice" data-testid="no-ups">{t('No UPS yet. Add a server and a UPS under Settings.')}</p>
{:else}
    <div class="nut-cards" data-testid="ups-cards">
        {#each st.ups as u (u.id)}
            <section class="nut-card" class:nut-card-critical={u.reasons.length > 0 && !u.suppressed} data-testid="ups-{u.name}">
                <header class="nut-card-head">
                    <h2>{u.label}</h2>
                    <span class="nut-muted nut-small">{u.mfr || ''} {u.model || ''} · {t('on {server}', {server: u.server_name || u.server})} · {u.name}</span>
                    <span class="nut-spacer"></span>
                    {#if !u.enabled}
                        <span class="nut-pill">{t('disabled')}</span>
                    {:else if u.suppressed}
                        <span class="nut-pill warn" title={t('ignored until mains returns')}>{t('ignored until mains returns')}</span>
                    {/if}
                    <span class="nut-pill" class:ok={u.connected} class:bad={!u.connected} title={u.error || ''} data-testid="conn">{u.connected ? t('connected') : t('not connected')}</span>
                </header>
                {#if u.error}<p class="nut-error" data-testid="ups-error">{u.error}</p>{/if}
                <div class="nut-flags" data-testid="flags">
                    {#if u.status.length === 0}
                        <span class="nut-muted">{t('no data yet')}</span>
                    {/if}
                    {#each u.status as f (f)}
                        <span class="nut-flag {flagClass(f)}" title={f}>{t('flag.' + f) === 'flag.' + f ? f : t('flag.' + f)} <span class="nut-faint">{f}</span></span>
                    {/each}
                </div>
                <div class="nut-meters">
                    <div class="nut-meter">
                        <span class="nut-meter-label">{t('Charge')}</span>
                        <span class="nut-meter-value {chargeClass(u)}" data-testid="charge">{withUnit(u.charge, '%')}</span>
                        <div class="nut-bar"><div class="nut-bar-fill {chargeClass(u)}" style="width: {Math.max(0, Math.min(100, u.charge ?? 0))}%"></div></div>
                    </div>
                    <div class="nut-meter">
                        <span class="nut-meter-label">{t('Runtime')}</span>
                        <span class="nut-meter-value" data-testid="runtime">{formatSeconds(u.runtime)}</span>
                    </div>
                    <div class="nut-meter">
                        <span class="nut-meter-label">{t('Load')}</span>
                        <span class="nut-meter-value">{withUnit(u.load, '%')}</span>
                        <div class="nut-bar"><div class="nut-bar-fill {u.load !== undefined && u.load > 90 ? 'bad' : ''}" style="width: {Math.max(0, Math.min(100, u.load ?? 0))}%"></div></div>
                    </div>
                    <div class="nut-meter">
                        <span class="nut-meter-label">{t('Input')}</span>
                        <span class="nut-meter-value">{withUnit(u.input_voltage, 'V')}</span>
                    </div>
                </div>
                <div class="nut-kv nut-kv-compact">
                    <span>{t('Output')}</span><span>{withUnit(u.output_voltage, 'V')}</span>
                    <span>{t('Battery')}</span><span>{withUnit(u.battery_voltage, 'V', 1)}</span>
                    {#if u.temperature !== undefined}<span>{t('Temperature')}</span><span>{withUnit(u.temperature, '°C', 1)}</span>{/if}
                    <span>{t('On battery for')}</span><span data-testid="on-battery">{u.on_battery ? formatSeconds(u.on_battery_s) : '–'}</span>
                    <span>{t('Data age')}</span><span>{u.data_age_s !== undefined ? t('{n} s', {n: u.data_age_s}) : '–'}</span>
                </div>
                {#if u.reasons.length > 0}
                    <div class="nut-reasons" data-testid="reasons">
                        <strong>{t('Rules firing')}:</strong>
                        <ul class="nut-list">
                            {#each u.reasons as r (r.rule)}<li>{reasonText(r, t)}</li>{/each}
                        </ul>
                    </div>
                {/if}
                {#if u.vars}
                    <details class="nut-details" bind:open={open[u.id]}>
                        <summary>{t('All variables')} ({Object.keys(u.vars).length})</summary>
                        <table class="nut-grid nut-grid-compact">
                            <thead><tr><th>{t('Variable')}</th><th>{t('Value')}</th></tr></thead>
                            <tbody>
                                {#each variables(u) as [k, v] (k)}<tr><td class="nut-mono">{k}</td><td>{v}</td></tr>{/each}
                            </tbody>
                        </table>
                    </details>
                {/if}
            </section>
        {/each}
    </div>
{/if}
