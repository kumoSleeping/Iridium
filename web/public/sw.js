// Web Push (plan "推送"): registered only once push is turned on in the settings. A push shows a
// notification; tapping it opens the session's chat, in a page already open if there is one.
self.addEventListener("push", (e) => {
  let d = {};
  try {
    d = e.data ? e.data.json() : {};
  } catch (_) {
    d = { title: e.data ? e.data.text() : "Iridium" };
  }
  e.waitUntil(self.registration.showNotification(d.title || "Iridium", { body: d.body || "", tag: d.session || undefined, data: { session: d.session || "" }, icon: "/favicon.svg" }));
});

self.addEventListener("notificationclick", (e) => {
  e.notification.close();
  const session = (e.notification.data && e.notification.data.session) || "";
  const url = session ? `/#/s/${encodeURIComponent(session)}` : "/";
  e.waitUntil(
    self.clients.matchAll({ type: "window", includeUncontrolled: true }).then((list) => {
      for (const c of list) {
        if (new URL(c.url).origin === location.origin && "focus" in c) {
          c.navigate(url);
          return c.focus();
        }
      }
      return self.clients.openWindow(url);
    }),
  );
});
