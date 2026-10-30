<script lang="ts">
    // A yes/no question in the kit's dialog (never window.confirm).
    import {Dialog} from 'occulite-ui';
    import {t} from 'occulite-ui/i18n';

    let {
        open,
        title,
        message,
        confirmLabel,
        danger = false,
        testId = 'confirm',
        onconfirm,
        onclose,
    }: {open: boolean; title: string; message: string; confirmLabel: string; danger?: boolean; testId?: string; onconfirm: () => void; onclose: () => void} = $props();
</script>

<Dialog {open} {title} width="28rem" {onclose} {testId}>
    <p>{message}</p>
    {#snippet buttons()}
        <button class="hmm-button" onclick={onclose}>{t('Cancel')}</button>
        <button class={'hmm-button ' + (danger ? 'nut-danger' : 'nut-primary')} onclick={onconfirm} data-testid="{testId}-yes">{confirmLabel}</button>
    {/snippet}
</Dialog>
