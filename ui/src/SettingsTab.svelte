<script lang="ts">
    // The shutdown settings, the servers and the UPS with their rules.
    import {untrack} from 'svelte';
    import {t} from 'occulite-ui/i18n';
    import type {NoticesStore} from 'occulite-ui/notices';

    import ConfirmDialog from './ConfirmDialog.svelte';
    import ServerDialog from './ServerDialog.svelte';
    import UPSDialog from './UPSDialog.svelte';
    import {del, post, put} from './api.js';
    import {rulesSummary, type Config, type Server, type Settings, type ShutdownRecord, type UPS} from './model.js';

    let {config, notices, onchanged}: {config: Config; notices: NoticesStore; onchanged: (c: Config) => void} = $props();

    let form = $state<Settings>(untrack(() => ({...config.settings})));
    let busy = $state(false);
    let serverOpen = $state(false);
    let editingServer = $state<Server | undefined>();
    let upsOpen = $state(false);
    let editingUPS = $state<UPS | undefined>();
    let removing = $state<{kind: 'server' | 'ups'; id: string; label: string} | undefined>();
    let confirmReal = $state(false);

    $effect(() => {
        form = {...config.settings};
    });

    const dirty = $derived(JSON.stringify(form) !== JSON.stringify(config.settings));

    async function save() {
        busy = true;
        try {
            onchanged(await put<Config>('api/v1/settings', form));
            notices.push('info', t('Saved.'));
        } catch (e) {
            notices.push('error', (e as Error).message);
        } finally {
            busy = false;
        }
    }

    async function test(real: boolean) {
        confirmReal = false;
        busy = true;
        try {
            const r = await post<ShutdownRecord>('api/v1/shutdown/test', {real});
            if (r.error) notices.push('error', t('The system refused: {error}', {error: r.error}));
            else if (real) notices.push('warn', t('Shutdown ordered.'));
            else notices.push('info', t('The dry run went through: see Events.'));
            onchanged(config);
        } catch (e) {
            notices.push('error', (e as Error).message);
        } finally {
            busy = false;
        }
    }

    async function remove() {
        const r = removing;
        removing = undefined;
        if (!r) return;
        try {
            onchanged(await del<Config>(r.kind === 'server' ? `api/v1/servers/${encodeURIComponent(r.id)}` : `api/v1/ups/${encodeURIComponent(r.id)}`));
            notices.push('info', t('Removed.'));
        } catch (e) {
            notices.push('error', (e as Error).message);
        }
    }

    function serverName(id: string): string {
        return config.servers.find((s) => s.id === id)?.name ?? id;
    }
</script>

<section class="nut-section" data-testid="shutdown">
    <h2>{t('Shutdown')}</h2>
    <div class="nut-form">
        <label class="nut-check"><input type="checkbox" bind:checked={form.enabled} data-testid="watch-enabled" /> {t('Watch the UPS and shut the system down when a rule fires')}</label>
        <p class="nut-help">{t('Off, the page still shows the UPS; nothing is ever shut down.')}</p>
        <label for="action">{t('Action')}</label>
        <select class="hmm-select" id="action" bind:value={form.action} data-testid="action">
            <option value="poweroff">{t('Power off')}</option>
            <option value="reboot">{t('Reboot')}</option>
        </select>
        <label for="grace">{t('Grace period (s)')}</label>
        <input class="hmm-input nut-narrow" id="grace" type="number" min="0" max="3600" bind:value={form.grace_s} data-testid="grace" />
        <p class="nut-help">{t('The countdown between the rule and the shutdown. It is cancelled when mains returns in time. A forced shutdown (FSD) ignores it.')}</p>
        <label class="nut-check"><input type="checkbox" bind:checked={form.dry_run} data-testid="dry-run" /> {t('Dry run: log and show what would happen, never shut down')}</label>
        {#if config.ups.length > 1}
            <label class="nut-check"><input type="checkbox" bind:checked={form.require_all} /> {t('Only when every UPS is critical (a system fed by two UPS)')}</label>
        {/if}
        <label for="poll">{t('Polling interval (s)')}</label>
        <input class="hmm-input nut-narrow" id="poll" type="number" min="1" max="300" bind:value={form.poll_s} />
        <label for="dead">{t('Dead time (s)')}</label>
        <input class="hmm-input nut-narrow" id="dead" type="number" min="5" max="3600" bind:value={form.dead_s} />
        <p class="nut-help">{t('How long the data may be missing before "communication lost" applies.')}</p>
    </div>
    <div class="nut-actions">
        <button class="hmm-button nut-primary" disabled={!dirty || busy} onclick={save} data-testid="settings-save">{t('Save')}</button>
        <span class="nut-spacer"></span>
        <button class="hmm-button" disabled={busy} onclick={() => test(false)} data-testid="test-dry">{t('Test (dry run)')}</button>
        <button class="hmm-button nut-danger" disabled={busy} onclick={() => (confirmReal = true)} data-testid="test-real">{config.settings.action === 'reboot' ? t('Reboot now…') : t('Shut down now…')}</button>
    </div>
    <p class="nut-help">{t("The addon shuts the system down through the system's own API (systemctl start poweroff.target). Its token needs the scope system:write, which the manifest declares; the Addons page shows it.")}</p>
</section>

<section class="nut-section" data-testid="servers">
    <div class="nut-section-head">
        <h2>{t('Servers')}</h2>
        <button class="hmm-button nut-primary" onclick={() => ((editingServer = undefined), (serverOpen = true))} data-testid="server-add">{t('Add server')}</button>
    </div>
    {#if config.servers.length === 0}
        <p class="nut-muted">{t('No servers.')}</p>
    {:else}
        <table class="nut-grid">
            <thead><tr><th>{t('Name')}</th><th>{t('Address')}</th><th>{t('Encryption')}</th><th>{t('User')}</th><th></th></tr></thead>
            <tbody>
                {#each config.servers as s (s.id)}
                    <tr>
                        <td>{s.name}</td>
                        <td class="nut-mono">{s.host}:{s.port}</td>
                        <td>{s.tls === 'starttls' ? 'STARTTLS' : '–'}</td>
                        <td>{s.username || '–'}</td>
                        <td class="nut-cell-actions">
                            <button class="hmm-button" onclick={() => ((editingServer = s), (serverOpen = true))}>{t('Edit')}</button>
                            <button class="hmm-button" onclick={() => (removing = {kind: 'server', id: s.id, label: s.name})}>{t('Remove')}</button>
                        </td>
                    </tr>
                {/each}
            </tbody>
        </table>
    {/if}
</section>

<section class="nut-section" data-testid="ups-list">
    <div class="nut-section-head">
        <h2>{t('UPS')}</h2>
        <button class="hmm-button nut-primary" onclick={() => ((editingUPS = undefined), (upsOpen = true))} disabled={config.servers.length === 0} data-testid="ups-add">{t('Add UPS')}</button>
    </div>
    {#if config.ups.length === 0}
        <p class="nut-muted">{config.servers.length === 0 ? t('Add a server first.') : t('No UPS.')}</p>
    {:else}
        <table class="nut-grid">
            <thead><tr><th>{t('Label')}</th><th>{t('Server')}</th><th>{t('UPS name on the server')}</th><th>{t('Rules')}</th><th>{t('Enabled')}</th><th></th></tr></thead>
            <tbody>
                {#each config.ups as u (u.id)}
                    <tr>
                        <td>{u.label}</td>
                        <td>{serverName(u.server)}</td>
                        <td class="nut-mono">{u.name}</td>
                        <td class="nut-small">{rulesSummary(u.rules, t)}</td>
                        <td>{u.enabled ? t('yes') : t('no')}</td>
                        <td class="nut-cell-actions">
                            <button class="hmm-button" onclick={() => ((editingUPS = u), (upsOpen = true))}>{t('Edit')}</button>
                            <button class="hmm-button" onclick={() => (removing = {kind: 'ups', id: u.id, label: u.label})}>{t('Remove')}</button>
                        </td>
                    </tr>
                {/each}
            </tbody>
        </table>
    {/if}
</section>

<ServerDialog open={serverOpen} server={editingServer} onclose={() => (serverOpen = false)} onsaved={(c) => ((serverOpen = false), onchanged(c))} />
<UPSDialog open={upsOpen} {config} ups={editingUPS} onclose={() => (upsOpen = false)} onsaved={(c) => ((upsOpen = false), onchanged(c))} />
<ConfirmDialog
    open={removing !== undefined}
    title={removing?.kind === 'server' ? t('Remove server {name}?', {name: removing?.label ?? ''}) : t('Remove UPS {name}?', {name: removing?.label ?? ''})}
    message={removing?.kind === 'server' ? t('The UPS on it are removed too.') : ''}
    confirmLabel={t('Remove')}
    danger
    testId="confirm-remove"
    onconfirm={remove}
    onclose={() => (removing = undefined)}
/>
<ConfirmDialog
    open={confirmReal}
    title={config.settings.action === 'reboot' ? t('Reboot the system now?') : t('Shut the system down now?')}
    message={config.settings.action === 'reboot' ? t('This tests the whole path for real: the system reboots now.') : t('This tests the whole path for real: the system powers off and does not come back until the power is cycled.')}
    confirmLabel={config.settings.action === 'reboot' ? t('Reboot') : t('Shut down')}
    danger
    testId="confirm-shutdown"
    onconfirm={() => test(true)}
    onclose={() => (confirmReal = false)}
/>
