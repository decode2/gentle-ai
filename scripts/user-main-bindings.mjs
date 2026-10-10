// Fixed declaration/supplier preflight, not source API or runtime qualification.
// The caller must authenticate stock acquisition and bind Main to its snapshot.
export const mainBindingFiles = Object.freeze([
  'package.json',
  'scripts/gentle-ai-installer.mjs',
  'runtime/gentle-ai-binary.mjs',
]);

export function assertMainBindings(main, stock) {
  for (const name of mainBindingFiles) {
    if (!Buffer.isBuffer(main[name]) || !Buffer.isBuffer(stock[name]) ||
        main[name].length > 33554432 || stock[name].length > 33554432 ||
        !main[name].equals(stock[name])) {
      throw Error(`Main stock binding differs: ${name}`);
    }
  }
}
