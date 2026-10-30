<script lang="ts">
    // Adds a UPS or changes one: the server, the UPS's name on it (discovered with LIST UPS, or
    // typed), the label, and the rules that shut the system down.
    import {Dialog} from 'occulite-ui';
    import {untrack} from 'svelte';
    import {t} from 'occulite-ui/i18n';

    import {post, put} from './api.js';
    import {emptyUPS, type Config, type ProbeResult, type UPS} from './model.js';

    let {open, config, ups, onclose, onsaved}: {open: boolean; config: Config; ups: UPS | undefined; onclose: () => void; onsaved: (c: Config) => void} = $props();

    let draft = $state<UPS>(untrack(() => emptyUPS('', config.defaults.rules)));
    let error = $state('');
    let busy = $state(false);
    let discovering = $state(false);
    let found = $state<{name: string; description: string}[]>([]);

    $effect(() => {
        if (!open) return;
        // the plain object, not the proxy: reading draft here would make the effect depend on
        // what it writes
        const d = ups ? {...ups, rules: {...ups.rules}} : emptyUPS(config.servers[0]?.id ?? '', config.defaults.rules);
        draft = d;
        error = '';
        found = [];
        if (!ups && d.server) untrack(() => void discover());
    });

    const isNew = $derived(ups === undefined);
    const valid = $derived(draft.server !== '' && /^[A-Za-z0-9_.-]{1,64}$/.test(draft.name));

    async function discover() {
        const s = config.servers.find((x) => x.id === draft.server);
        if (!s) return;
        discovering = true;
        error = '';
        try {
            const r = await post<ProbeResult>('api/v1/servers/probe', {...s});
            found = r.ups;
            if (!draft.name && found.length === 1) draft.name = found[0]!.name;
        } catch (e) {
            error = (e as Error).message;
        } finally {
            discovering = false;
        }
    }

    async function save() {
        busy = true;
        error = '';
        try {
            const body = {...draft, label: draft.label.trim() || draft.name};
            const c = isNew ? await post<Config>('api/v1/ups', body) : await put<Config>(`api/v1/ups/${encodeURIComponent(ups!.id)}`, body);
            onsaved(c);
        } catch (e) {
            error = (e as Error).message;
        } finally {
            busy = false;
        }
    }
</script>

<Dialog {open} title={isNew ? t('New UPS') : (ups?.label ?? '')} width="36rem" closable={!busy} {onclose} testId="ups-dialog">
    <div class="nut-form">
        <label for="u-server">{t('Server')}</label>
        <select class="hmm-select" id="u-server" bind:value={draft.server} onchange={() => ((found = []), void discover())} data-testid="ups-server">
            {#each config.servers as s (s.id)}<option value={s.id}>{s.name} ({s.host}:{s.port})</option>{/each}
        </select>
        <label for="u-name">{t('UPS name on the server')}</label>
        <div class="nut-row">
            {#if found.length > 0}
                <select class="hmm-select" id="u-name" bind:value={draft.name} data-testid="ups-name-select">
                    {#each found as f (f.name)}<option value={f.name}>{f.name}{f.description ? ' – ' + f.description : ''}</option>{/each}
                    {#if draft.name && !found.some((f) => f.name === draft.name)}<option value={draft.name}>{draft.name}</option>{/if}
                </select>
            {:else}
                <input class="hmm-input" id="u-name" type="text" bind:value={draft.name} maxlength="64" placeholder={t('the name in ups.conf, e.g. eaton')} autocomplete="off" data-testid="ups-name" />
            {/if}
            <button class="hmm-button nut-fixed" onclick={discover} disabled={discovering || !draft.server} data-testid="ups-discover">{discovering ? t('Testing…') : t('Discover')}</button>
        </div>
        <label for="u-label">{t('Label')}</label>
        <input class="hmm-input" id="u-label" type="text" bind:value={draft.label} maxlength="64" placeholder={draft.name || t('as shown on the page')} data-testid="ups-label" />
        <label class="nut-check"><input type="checkbox" bind:checked={draft.enabled} /> {t('Watch this UPS')}</label>
        <span class="nut-label nut-span">{t('Shut the system down when this UPS…')}</span>
        <div class="nut-checklist nut-span">
            <label class="nut-check"><input type="checkbox" bind:checked={draft.rules.low_battery} data-testid="rule-lb" /> {t('rule.low_battery')}</label>
            <label class="nut-check"><input type="checkbox" bind:checked={draft.rules.fsd} /> {t('rule.fsd')}</label>
            <label class="nut-check">
                <input type="checkbox" checked={draft.rules.charge_below > 0} onchange={(e) => (draft.rules.charge_below = (e.currentTarget as HTMLInputElement).checked ? 20 : 0)} data-testid="rule-charge" />
                {t('rule.charge_below').split('…')[0]}
                <input class="hmm-input nut-narrow" type="number" min="0" max="100" bind:value={draft.rules.charge_below} title={t('0 = off')} data-testid="rule-charge-n" />
                {t('rule.charge_below').split('…')[1]}
            </label>
            <label class="nut-check">
                <input type="checkbox" checked={draft.rules.runtime_below > 0} onchange={(e) => (draft.rules.runtime_below = (e.currentTarget as HTMLInputElement).checked ? 300 : 0)} />
                {t('rule.runtime_below').split('…')[0]}
                <input class="hmm-input nut-narrow" type="number" min="0" max="86400" bind:value={draft.rules.runtime_below} title={t('0 = off')} />
                {t('rule.runtime_below').split('…')[1]}
            </label>
            <label class="nut-check">
                <input type="checkbox" checked={draft.rules.on_battery_longer > 0} onchange={(e) => (draft.rules.on_battery_longer = (e.currentTarget as HTMLInputElement).checked ? 600 : 0)} />
                {t('rule.on_battery_longer').split('…')[0]}
                <input class="hmm-input nut-narrow" type="number" min="0" max="86400" bind:value={draft.rules.on_battery_longer} title={t('0 = off')} />
                {t('rule.on_battery_longer').split('…')[1]}
            </label>
            <label class="nut-check"><input type="checkbox" bind:checked={draft.rules.comm_lost} /> {t('rule.comm_lost')}</label>
        </div>
    </div>
    <p class="nut-error" role="alert">{error}</p>
    {#snippet buttons()}
        <button class="hmm-button" onclick={onclose} disabled={busy}>{t('Cancel')}</button>
        <button class="hmm-button nut-primary" onclick={save} disabled={!valid || busy} data-testid="ups-save">{t('Save')}</button>
    {/snippet}
</Dialog>
