<script lang="ts">
    // The page: the UPS's state, the settings (servers, UPS, rules, the shutdown), the events. A
    // running countdown shows as a banner on every tab, with its cancel button.
    import {onMount} from 'svelte';
    import {Notices, Tabs, ThemeSwitch} from 'occulite-ui';
    import {i18n, t} from 'occulite-ui/i18n';
    import {NoticesStore} from 'occulite-ui/notices';
    import {cycleTheme, theme} from 'occulite-ui/theme';

    import EventsTab from './EventsTab.svelte';
    import SettingsTab from './SettingsTab.svelte';
    import StatusTab from './StatusTab.svelte';
    import {get, post} from './api.js';
    import type {Config, Status} from './model.js';

    const notices = new NoticesStore();
    let tab = $state('status');
    let config = $state<Config | undefined>();
    let status = $state<Status | undefined>();
    let loadError = $state('');
    let now = $state(Date.now());

    async function loadConfig() {
        try {
            config = await get<Config>('api/v1/config');
            loadError = '';
        } catch (e) {
            loadError = (e as Error).message;
        }
    }

    async function loadStatus() {
        try {
            status = await get<Status>('api/v1/status');
        } catch (e) {
            loadError = (e as Error).message;
        }
    }

    onMount(() => {
        void loadConfig();
        void loadStatus();
        const poll = setInterval(() => void loadStatus(), 2000);
        const tick = setInterval(() => (now = Date.now()), 500);
        return () => {
            clearInterval(poll);
            clearInterval(tick);
        };
    });

    function changed(c: Config) {
        config = c;
        void loadStatus();
    }

    async function cancel() {
        try {
            await post('api/v1/shutdown/cancel');
            notices.push('warn', t('The countdown was cancelled. The UPS that caused it is ignored until mains returns.'));
            await loadStatus();
        } catch (e) {
            notices.push('error', (e as Error).message);
        }
    }

    // the countdown counts with the server's clock: its "now" plus the time since the answer
    let loaded = $state(Date.now());
    $effect(() => {
        if (status) loaded = Date.now();
    });
    const countdown = $derived(status?.state.countdown);
    const remaining = $derived(countdown ? Math.max(0, Math.ceil((countdown.until - (status!.now + (now - loaded))) / 1000)) : 0);

    const tabs = $derived([
        {id: 'status', label: t('Status'), ...(countdown ? {badge: '!'} : {})},
        {id: 'settings', label: t('Settings'), ...(config && config.ups.length === 0 ? {badge: '!'} : {})},
        {id: 'events', label: t('Events')},
    ]);
</script>

<div class="nut-app" lang={i18n.language}>
    <header class="nut-header">
        <h1>{t('UPS')}</h1>
        <Tabs {tabs} bind:active={tab} label={t('UPS')} testId="tabs" />
        <span class="nut-spacer"></span>
        {#if config}<span class="nut-muted nut-small">{t('Version {v}', {v: config.version})}</span>{/if}
        {#if !theme.followsShell}
            <ThemeSwitch theme={theme.choice} onclick={cycleTheme} />
        {/if}
    </header>
    {#if loadError}
        <div class="nut-banner" role="alert">{t('The page could not read the configuration: {error}', {error: loadError})}</div>
    {/if}
    {#if countdown}
        <div class="nut-countdown" role="alert" data-testid="countdown">
            <strong>{countdown.dry_run ? t('Dry run: ') : ''}{t('{action} in {n} s', {action: t('action.' + countdown.action), n: remaining})}</strong>
            <span class="nut-muted">{countdown.reasons.join(', ')}</span>
            <span class="nut-spacer"></span>
            <button class="hmm-button nut-danger" onclick={cancel} data-testid="countdown-cancel">{t('Cancel the countdown')}</button>
        </div>
    {:else if status?.state.issued && status.state.shutdown && !status.state.shutdown.dry_run}
        <div class="nut-countdown" role="alert" data-testid="issued">
            <strong>{t('The shutdown was ordered at {time}. The system is going down.', {time: new Date(status.state.shutdown.at).toLocaleTimeString()})}</strong>
        </div>
    {/if}
    <main class="nut-page">
        {#if config}
            {#if tab === 'status'}
                <StatusTab {config} {status} onrecheck={loadStatus} />
            {:else if tab === 'settings'}
                <SettingsTab {config} {notices} onchanged={changed} />
            {:else}
                <EventsTab {notices} />
            {/if}
        {:else if !loadError}
            <p class="nut-muted">{t('Loading…')}</p>
        {/if}
    </main>
    <Notices notices={notices.visible} ondismiss={(id) => notices.dismiss(id)} />
</div>
