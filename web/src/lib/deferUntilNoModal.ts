/** Automatic guidance must not compete with an account/setup/edit dialog. */
export function deferUntilNoModal(run: () => void, delay = 600) {
  let timer: ReturnType<typeof setTimeout>;
  const attempt = () => {
    if (document.querySelector('[role="dialog"], dialog[open]')) {
      timer = setTimeout(attempt, delay);
    } else {
      run();
    }
  };
  timer = setTimeout(attempt, delay);
  return () => clearTimeout(timer);
}
