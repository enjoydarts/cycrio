const confirmationURL = self.location.origin + '/settings/notifications';

async function forwardProof(data) {
  const { cycrio_challenge_id: id, cycrio_challenge_secret: secret } =
    data ?? {};
  if (
    typeof id !== 'string' ||
    !/^[0-9a-f]{8}(?:-[0-9a-f]{4}){3}-[0-9a-f]{12}$/i.test(id) ||
    typeof secret !== 'string' ||
    !/^[A-Za-z0-9_-]{42}[AEIMQUYcgkosw048]$/.test(secret)
  )
    return;
  const windows = await self.clients.matchAll({
    type: 'window',
    includeUncontrolled: true,
  });
  for (const client of windows)
    client.postMessage({
      type: 'cycrio.push.proof',
      proof: { cycrio_challenge_id: id, cycrio_challenge_secret: secret },
    });
}

// OneSignalの表示と遷移を維持し、受信証明だけを登録操作中の画面へ渡す。
self.addEventListener('install', (event) =>
  event.waitUntil(self.skipWaiting()),
);
self.addEventListener('push', (event) => {
  let custom;
  try {
    custom = event.data?.json()?.custom;
  } catch {
    return;
  }
  if (custom?.u === confirmationURL) event.waitUntil(forwardProof(custom.a));
});
self.addEventListener('notificationclick', (event) => {
  const notification = event.notification.data;
  if (notification?.launchURL === confirmationURL)
    event.waitUntil(forwardProof(notification.additionalData));
});
