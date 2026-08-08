export const COMMAND_HISTORY_LIMIT = 30;

export function addCommandHistory(history, value) {
  const command = String(value ?? "").trim();
  if (!command) return [...history];
  return [command, ...history.filter((item) => item !== command)].slice(0, COMMAND_HISTORY_LIMIT);
}

export function navigateCommandHistory(history, navigation, direction, currentValue) {
  if (!history.length) return { navigation, value: currentValue };
  let { index, draft } = navigation;
  if (direction < 0) {
    if (index < 0) draft = currentValue;
    index = Math.min(index + 1, history.length - 1);
    return { navigation: { index, draft }, value: history[index] };
  }
  if (index < 0) return { navigation, value: currentValue };
  if (index === 0) return { navigation: { index: -1, draft: "" }, value: draft };
  index -= 1;
  return { navigation: { index, draft }, value: history[index] };
}
