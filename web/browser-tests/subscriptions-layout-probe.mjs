import assert from 'node:assert/strict';

export async function runSubscriptionsLayoutProbe(
  api,
  session,
  visit,
  js,
  wait,
  layout,
  click,
  screenshot,
) {
  for (const width of [
    320, 375, 390, 430, 650, 700, 768, 850, 1100, 1101, 1280,
  ]) {
    await api('POST', session + '/goog/cdp/execute', {
      cmd: 'Emulation.setDeviceMetricsOverride',
      params: {
        width,
        height: 1100,
        deviceScaleFactor: 1,
        mobile: width <= 700,
      },
    });
    await visit('mobile-subscriptions', width);
    await wait(
      'document.querySelectorAll(".table-SubscriptionListPanel tbody tr").length===4',
    );
    await layout();
    if (width <= 1100) {
      assert.equal(
        await js(
          `return [...document.querySelectorAll('.table-SubscriptionListPanel td')].filter(e=>e.getClientRects().length).every(e=>getComputedStyle(e).paddingLeft==='0px' && getComputedStyle(e).borderTopWidth==='0px');`,
        ),
        true,
        'Mobile rows must not retain desktop cell padding or dividers',
      );
      assert.equal(
        await js(
          `const table=document.querySelector('.table-SubscriptionListPanel').parentElement; return table.scrollWidth<=table.clientWidth;`,
        ),
        true,
        'Mobile list must not scroll horizontally inside its panel',
      );
    }
    const rows =
      await js(`return [...document.querySelectorAll('.table-SubscriptionListPanel tbody tr')].map(row => {
      const service=row.querySelector('.service').getBoundingClientRect();
      const name=row.querySelector('.brand-text').getBoundingClientRect();
      const price=row.querySelector('.price').getBoundingClientRect();
      const date=row.querySelector('.date').getBoundingClientRect();
      return {nameWidth:name.width, serviceBottom:service.bottom, priceTop:price.top, priceBottom:price.bottom, dateTop:date.top};
    });`);
    if (width <= 1100) {
      for (const row of rows) {
        assert.ok(
          row.nameWidth >= 150,
          `Service information must remain readable at ${width}px: ${JSON.stringify(row)}`,
        );
        assert.ok(
          row.priceTop >= row.serviceBottom - 1,
          `Price must follow service information at ${width}px`,
        );
        assert.ok(
          row.dateTop >= row.priceBottom - 1,
          `Billing date must follow price at ${width}px`,
        );
      }
    } else {
      assert.ok(
        rows.every((row) => row.priceTop < row.serviceBottom),
        'Desktop list must retain columns',
      );
    }
    await screenshot(`subscriptions-list-${width}`);
    await click('document.querySelector("button[aria-label=カード表示]")');
    await wait('document.querySelector(".cards")!==null');
    await layout();
    await screenshot(`subscriptions-cards-${width}`);
    await visit('mobile-overview', width);
    await wait(
      'document.querySelector(".table-OverviewPanel tbody tr")!==null',
    );
    if (width <= 700) {
      const upcoming =
        await js(`const row=document.querySelector('.table-OverviewPanel tbody tr');
        const service=row.querySelector('[data-primary]').getBoundingClientRect();
        const amount=row.querySelector('[data-label="登録料金"]').getBoundingClientRect();
        return {serviceWidth:service.width, serviceBottom:service.bottom, amountTop:amount.top};`);
      assert.ok(
        upcoming.amountTop >= upcoming.serviceBottom - 1,
        'Home billing information must follow service information on mobile',
      );
      assert.ok(
        upcoming.serviceWidth >= 200,
        'Home service information must use the available mobile width',
      );
    }
    await layout();
    await screenshot(`overview-${width}`);
  }
  return { scenario: 'subscriptions-responsive-layout', status: 'pass' };
}
