<script lang="ts">
    // The event log: status changes, countdowns, shutdowns, newest first.
    import {t} from 'occulite-ui/i18n';
    import type {NoticesStore} from 'occulite-ui/notices';

    import ConfirmDialog from './ConfirmDialog.svelte';
    import {del, get} from './api.js';
    import {eventText, type Event} from './model.js';

    let {notices}: {notices: NoticesStore} = $props();
    let events = $state<Event[]>([]);
    let error = $state('');
    let confirmClear = $state(false);

    async function load() {
        try {
            events = (await get<Event[]>('api/v1/events')).slice().reverse();
            error = '';
        } catch (e) {
            error = (e as Error).message;
        }
    }

    $effect(() => {
        void load();
        const id = setInterval(() => void load(), 3000);
        return () => clearInterval(id);
    });

    async function clear() {
        confirmClear = false;
        try {
            await del('api/v1/events');
            await load();
        } catch (e) {
            notices.push('error', (e as Error).message);
        }
    }

    function kindClass(k: string): string {
        return k === 'shutdown' || k === 'error' ? 'bad' : k === 'countdown' || k === 'comm_lost' || k === 'dry_run' ? 'warn' : '';
    }
</script>

<section class="nut-section nut-section-wide" data-testid="events">
    <div class="nut-section-head">
        <h2>{t('Events')}</h2>
        <button class="hmm-button" onclick={() => (confirmClear = true)} disabled={events.length === 0}>{t('Clear')}</button>
    </div>
    <p class="nut-help">{t('The last 200 events, kept across a restart: after a shutdown this says why.')}</p>
    {#if error}<p class="nut-error">{error}</p>{/if}
    {#if events.length === 0}
        <p class="nut-muted">{t('No events yet.')}</p>
    {:else}
        <table class="nut-grid nut-grid-compact">
            <thead><tr><th>{t('Time')}</th><th>{t('UPS')}</th><th>{t('Event')}</th></tr></thead>
            <tbody>
                {#each events as e (e.at + e.kind + (e.ups ?? ''))}
                    <tr class={kindClass(e.kind)}>
                        <td class="nut-nowrap">{new Date(e.at).toLocaleString()}</td>
                        <td>{e.label ?? ''}</td>
                        <td>{eventText(e, t)}</td>
                    </tr>
                {/each}
            </tbody>
        </table>
    {/if}
</section>

<ConfirmDialog open={confirmClear} title={t('Clear')} message={t('Events')} confirmLabel={t('Clear')} testId="confirm-clear" onconfirm={clear} onclose={() => (confirmClear = false)} />
