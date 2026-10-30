import {expect, test} from '@playwright/test';

// The readme's screenshots (SCREENSHOTS=1 npx playwright test screenshots): German, light and dark.
test.skip(!process.env.SCREENSHOTS, 'only on demand');
test.use({locale: 'de-DE', viewport: {width: 1200, height: 820}});

const FAKE = 'http://127.0.0.1:18080';

for (const scheme of ['light', 'dark'] as const) {
    test(`screenshots ${scheme}`, async ({page}) => {
        await page.emulateMedia({colorScheme: scheme});
        const suffix = scheme === 'dark' ? '-dunkel' : '';
        const out = (n: string) => `../docs/screenshots/${n}${suffix}.png`;
        await page.goto('./');
        await page.getByRole('tab', {name: /Einstellungen/}).click();
        if (!(await page.getByTestId('servers').getByText('13493').count())) {
            await page.getByTestId('server-add').click();
            await page.getByTestId('server-name').fill('NAS');
            await page.getByTestId('server-host').fill('192.168.1.10');
            await page.getByTestId('server-port').fill('13493');
            await page.getByTestId('server-host').fill('127.0.0.1');
            await page.getByTestId('server-user').fill('monuser');
            await page.getByTestId('server-password').fill('secret');
            await page.getByTestId('server-save').click();
            await page.getByTestId('ups-add').click();
            await page.getByTestId('ups-name-select').selectOption('eaton');
            await page.getByTestId('ups-label').fill('USV Keller');
            await page.getByTestId('rule-charge').check();
            await page.getByTestId('rule-charge-n').fill('30');
            await page.getByTestId('ups-save').click();
            await page.getByTestId('ups-add').click();
            await page.getByTestId('ups-name-select').selectOption('apc');
            await page.getByTestId('ups-label').fill('USV Büro');
            await page.getByTestId('ups-save').click();
            await page.getByTestId('dry-run').check();
            await page.getByTestId('settings-save').click();
        }
        await page.getByRole('tab', {name: /Status/}).click();
        await page.request.get(`${FAKE}/dev/ups?name=eaton&status=OL+CHRG&charge=100&runtime=1860`);
        await page.request.get(`${FAKE}/dev/ups?name=apc&status=OL&charge=97&runtime=1200`);
        await page.waitForTimeout(1500);
        await page.screenshot({path: out('01-status')});
        await page.request.get(`${FAKE}/dev/ups?name=eaton&status=OB+DISCHRG&charge=24&runtime=420&input.voltage=0`);
        await expect(page.getByTestId('countdown')).toBeVisible({timeout: 15_000});
        await page.waitForTimeout(800);
        await page.screenshot({path: out('02-countdown')});
        await page.request.get(`${FAKE}/dev/ups?name=eaton&status=OL+CHRG&charge=25&input.voltage=231.0`);
        await expect(page.getByTestId('countdown')).toHaveCount(0, {timeout: 15_000});
        await page.getByRole('tab', {name: /Einstellungen/}).click();
        await page.waitForTimeout(300);
        await page.screenshot({path: out('03-einstellungen')});
        await page.getByTestId('ups-list').getByRole('button', {name: 'Bearbeiten'}).first().click();
        await page.waitForTimeout(500);
        await page.screenshot({path: out('04-usv')});
        await page.keyboard.press('Escape');
        await page.getByRole('tab', {name: /Ereignisse/}).click();
        await page.waitForTimeout(500);
        await page.screenshot({path: out('05-ereignisse')});
    });
}
