import {expect, test, type Page} from '@playwright/test';

// One run through the page against the daemon on the fake system and the fake upsd: a server with
// its Test button, a UPS discovered on it, the status card, a low battery in dry run with the
// countdown and its cancel, the events. The steps depend on each other (one daemon, one
// configuration).
test.describe.configure({mode: 'serial'});

const FAKE = 'http://127.0.0.1:18080';

async function ups(page: Page, q: string) {
    const res = await page.request.get(`${FAKE}/dev/ups?name=eaton&${q}`);
    expect(res.ok()).toBeTruthy();
}

test('starts without a UPS and says so', async ({page}) => {
    await page.goto('./');
    await expect(page.getByTestId('no-ups')).toBeVisible();
    await expect(page.getByTestId('right')).toHaveText('granted');
});

test('adds a server after testing the connection', async ({page}) => {
    await page.goto('./');
    await page.getByRole('tab', {name: /Settings/}).click();
    await page.getByTestId('server-add').click();
    await page.getByTestId('server-name').fill('Lab NAS');
    await page.getByTestId('server-host').fill('127.0.0.1');
    await page.getByTestId('server-port').fill('13493');
    await page.getByTestId('server-user').fill('monuser');
    await page.getByTestId('server-password').fill('secret');
    await page.getByTestId('server-port').fill('1');
    await page.getByTestId('server-test').click();
    await expect(page.getByTestId('server-error')).toContainText('connection refused');
    await page.getByTestId('server-port').fill('13493');
    await page.getByTestId('server-test').click();
    await expect(page.getByTestId('probe-result')).toContainText('eaton');
    await expect(page.getByTestId('probe-result')).toContainText('apc');
    await page.getByTestId('server-save').click();
    await expect(page.getByTestId('servers')).toContainText('127.0.0.1:13493');
});

test('adds a UPS discovered on the server with a charge rule', async ({page}) => {
    await page.goto('./');
    await page.getByRole('tab', {name: /Settings/}).click();
    await page.getByTestId('ups-add').click();
    const sel = page.getByTestId('ups-name-select');
    await expect(sel).toBeVisible();
    await sel.selectOption('eaton');
    await page.getByTestId('ups-label').fill('Cellar');
    await page.getByTestId('rule-charge').check();
    await page.getByTestId('rule-charge-n').fill('50');
    await page.getByTestId('ups-save').click();
    await expect(page.getByTestId('ups-list')).toContainText('Cellar');
    await expect(page.getByTestId('ups-list')).toContainText('charge < 50 %');
});

test('the status card shows the UPS on mains', async ({page}) => {
    await page.goto('./');
    const card = page.getByTestId('ups-eaton');
    await expect(card).toBeVisible();
    await expect(card.getByTestId('conn')).toHaveText('connected');
    await expect(card.getByTestId('charge')).toHaveText('100 %');
    await expect(card.getByTestId('flags')).toContainText('On line');
    await expect(card.getByTestId('on-battery')).toHaveText('–');
});

test('dry run: a low charge on battery starts a countdown that cancels when mains returns', async ({page}) => {
    await page.goto('./');
    await page.getByRole('tab', {name: /Settings/}).click();
    await page.getByTestId('dry-run').check();
    await page.getByTestId('grace').fill('120');
    await page.getByTestId('settings-save').click();
    await expect(page.getByTestId('watch-state')).toHaveCount(0);
    await ups(page, 'status=OB+DISCHRG&charge=40&runtime=600');
    await page.getByRole('tab', {name: /Status/}).click();
    await expect(page.getByTestId('watch-state')).toHaveText('dry run');
    await expect(page.getByTestId('countdown')).toBeVisible({timeout: 15_000});
    await expect(page.getByTestId('countdown')).toContainText('Dry run: Power off in');
    await expect(page.getByTestId('reasons')).toContainText('charge 40 % below 50 %');
    await expect(page.getByTestId('ups-eaton').getByTestId('flags')).toContainText('On battery');
    await ups(page, 'status=OL+CHRG&charge=41');
    await expect(page.getByTestId('countdown')).toHaveCount(0, {timeout: 15_000});
    await page.getByRole('tab', {name: /Events/}).click();
    await expect(page.getByTestId('events')).toContainText('Countdown cancelled: the condition cleared');
    await expect(page.getByTestId('events')).toContainText('Status OL CHRG → OB DISCHRG');
});

test('the user cancels a countdown and the UPS is ignored until mains returns', async ({page}) => {
    await page.goto('./');
    await ups(page, 'status=OB+DISCHRG+LB&charge=5&runtime=60');
    await expect(page.getByTestId('countdown')).toBeVisible({timeout: 15_000});
    await page.getByTestId('countdown-cancel').click();
    await expect(page.getByTestId('countdown')).toHaveCount(0);
    await expect(page.getByTestId('ups-eaton')).toContainText('ignored until mains returns');
    await ups(page, 'status=OL+CHRG&charge=6');
    await expect(page.getByTestId('ups-eaton')).not.toContainText('ignored until mains returns', {timeout: 15_000});
});

test('the dry-run test button writes an event; the real one calls the system', async ({page}) => {
    await page.goto('./');
    await page.getByRole('tab', {name: /Settings/}).click();
    await page.getByTestId('test-dry').click();
    await expect(page.getByText('The dry run went through: see Events.')).toBeVisible();
    await page.getByTestId('test-real').click();
    await expect(page.getByTestId('confirm-shutdown')).toContainText('Shut the system down now?');
    await page.getByTestId('confirm-shutdown-yes').click();
    await expect(page.getByText('Shutdown ordered.')).toBeVisible();
    const calls = await (await page.request.get(`${FAKE}/dev/calls`)).json();
    expect(calls).toEqual(['poweroff.target start']);
    await expect(page.getByTestId('issued')).toBeVisible();
});

test.describe('in German', () => {
    test.use({locale: 'de-DE'});
    test('the page speaks German', async ({page}) => {
        await page.goto('./');
        await expect(page.getByRole('tab', {name: /Einstellungen/})).toBeVisible();
        await expect(page.getByTestId('watch')).toContainText('Recht zum Herunterfahren');
    });
});
