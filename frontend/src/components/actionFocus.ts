// Menus unmount their items before the editor opens. Keep the persistent trigger.
let actionTrigger: HTMLElement | null = null;
export function rememberActionTrigger(element: HTMLElement | null) {
  actionTrigger = element;
}
export function restoreActionTrigger() {
  const element = actionTrigger;
  actionTrigger = null;
  if (!element?.isConnected) return false;
  element.focus({ preventScroll: true });
  return true;
}
