<script lang="ts">
    // Adds an upsd server or changes one, with a Test connection button that lists its UPS. The
    // password is write-only: an empty field on an update keeps the stored one.
    import {Dialog} from 'occulite-ui';
    import {t} from 'occulite-ui/i18n';

    import {post, put} from './api.js';
    import {emptyServer, type Config, type ProbeResult, type Server} from './model.js';

    let {open, server, onclose, onsaved}: {open: boolean; server: Server | undefined; onclose: () => void; onsaved: (c: Config) => void} = $props();

    let draft = $state<Server>(emptyServer());
    let error = $state('');
    let probe = $state<ProbeResult | undefined>();
    let busy = $state(false);
    let testing = $state(false);

    $effect(() => {
        if (!open) return;
        draft = server ? {...server, password: '', ca_cert: server.ca_cert ?? ''} : emptyServer();
        error = '';
        probe = undefined;
    });

    const isNew = $derived(server === undefined);
    const valid = $derived(draft.host.trim() !== '' && draft.port >= 1 && draft.port <= 65535 && (draft.username === '' || draft.password !== '' || !isNew));

    function body(): Server {
        const b: Server = {...draft, host: draft.host.trim(), name: draft.name.trim() || draft.host.trim()};
        if (!b.password) delete b.password;
        if (!b.ca_cert) delete b.ca_cert;
        return b;
    }

    async function test() {
        testing = true;
        error = '';
        probe = undefined;
        try {
            probe = await post<ProbeResult>('api/v1/servers/probe', {...body(), id: server?.id ?? ''});
        } catch (e) {
            error = (e as Error).message;
        } finally {
            testing = false;
        }
    }

    async function save() {
        busy = true;
        error = '';
        try {
            const c = isNew ? await post<Config>('api/v1/servers', body()) : await put<Config>(`api/v1/servers/${encodeURIComponent(server!.id)}`, body());
            onsaved(c);
        } catch (e) {
            error = (e as Error).message;
        } finally {
            busy = false;
        }
    }
</script>

<Dialog {open} title={isNew ? t('New server') : t('Server {name}', {name: server?.name ?? ''})} width="36rem" closable={!busy} {onclose} testId="server-dialog">
    <p class="nut-help">{t('The upsd that has the UPS: a NAS, a Proxmox host, a Raspberry Pi at the USB cable. Its upsd.conf must LISTEN on an address this system reaches.')}</p>
    <div class="nut-form">
        <label for="sv-name">{t('Name')}</label>
        <input class="hmm-input" id="sv-name" type="text" bind:value={draft.name} maxlength="64" placeholder={draft.host} data-testid="server-name" />
        <label for="sv-host">{t('Host')}</label>
        <input class="hmm-input" id="sv-host" type="text" bind:value={draft.host} maxlength="253" autocomplete="off" data-testid="server-host" />
        <label for="sv-port">{t('Port')}</label>
        <input class="hmm-input nut-narrow" id="sv-port" type="number" min="1" max="65535" bind:value={draft.port} data-testid="server-port" />
        <label for="sv-tls">{t('Encryption')}</label>
        <select class="hmm-select" id="sv-tls" bind:value={draft.tls} data-testid="server-tls">
            <option value="off">{t('Plain (default)')}</option>
            <option value="starttls">{t('STARTTLS (required)')}</option>
        </select>
        {#if draft.tls === 'starttls'}
            <label class="nut-check"><input type="checkbox" bind:checked={draft.tls_verify} data-testid="server-verify" /> {t('Verify the server certificate')}</label>
            <p class="nut-help">{t('Off: any certificate is accepted (a self-signed upsd). On: the certificate must chain to the system roots or to the CA below and name the host.')}</p>
            {#if draft.tls_verify}
                <label for="sv-ca">{t('CA certificate (PEM, optional)')}</label>
                <textarea class="hmm-input nut-mono" id="sv-ca" rows="4" bind:value={draft.ca_cert} spellcheck="false"></textarea>
            {/if}
        {/if}
        <label for="sv-user">{t('User name (upsd.users, optional)')}</label>
        <input class="hmm-input" id="sv-user" type="text" bind:value={draft.username} maxlength="64" autocomplete="off" data-testid="server-user" />
        <label for="sv-pw">{t('Password')}</label>
        <input class="hmm-input" id="sv-pw" type="password" bind:value={draft.password} maxlength="128" autocomplete="new-password" placeholder={isNew || !server?.username ? '' : t('unchanged')} data-testid="server-password" />
        <p class="nut-help">{t('Reading needs no login on most servers; the password is stored on this system and never shown again.')}</p>
    </div>
    <div class="nut-row nut-probe">
        <button class="hmm-button nut-fixed" onclick={test} disabled={!valid || testing || busy} data-testid="server-test">{testing ? t('Testing…') : t('Test connection')}</button>
        {#if probe}
            <span class="nut-ok" data-testid="probe-result">
                {t('Connected: {version}', {version: probe.version})}{probe.tls ? ' · ' + t('encrypted') : ''}<br />
                {probe.ups.length ? t('UPS on this server: {list}', {list: probe.ups.map((u) => `${u.name} (${u.description})`).join(', ')}) : t('No UPS on this server.')}
            </span>
        {/if}
    </div>
    <p class="nut-error" role="alert" data-testid="server-error">{error}</p>
    {#snippet buttons()}
        <button class="hmm-button" onclick={onclose} disabled={busy}>{t('Cancel')}</button>
        <button class="hmm-button nut-primary" onclick={save} disabled={!valid || busy} data-testid="server-save">{t('Save')}</button>
    {/snippet}
</Dialog>
