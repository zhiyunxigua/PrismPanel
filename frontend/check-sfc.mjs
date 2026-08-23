// SFC 语法校验：遍历目录下所有 .vue，用 @vue/compiler-sfc 编译 script/scriptSetup + template。
// 用法：node check-sfc.mjs [目录]   （默认 frontend/src）
import { parse, compileScript, compileTemplate } from "@vue/compiler-sfc";
import { readFileSync, readdirSync, statSync } from "node:fs";
import { join, resolve } from "node:path";

const root = resolve(process.argv[2] || "src");
const files = [];
function walk(dir) {
  for (const entry of readdirSync(dir)) {
    const full = join(dir, entry);
    const stat = statSync(full);
    if (stat.isDirectory()) walk(full);
    else if (entry.endsWith(".vue")) files.push(full);
  }
}
walk(root);

let failed = 0;
for (const file of files) {
  const source = readFileSync(file, "utf8");
  const { descriptor, errors } = parse(source, { filename: file });
  const errs = [...errors];
  try {
    if (descriptor.script || descriptor.scriptSetup) {
      compileScript(descriptor, { id: "check" });
    }
  } catch (error) {
    errs.push(error);
  }
  try {
    if (descriptor.template) {
      compileTemplate({
        source: descriptor.template.content,
        filename: file,
        id: "check",
        compilerOptions: { bindingMetadata: undefined },
      });
    }
  } catch (error) {
    errs.push(error);
  }
  if (errs.length) {
    failed += 1;
    console.error(`FAIL ${file}`);
    for (const error of errs) console.error("   -", error.message || String(error));
  } else {
    console.log(`OK   ${file}`);
  }
}
console.log(`\n${files.length} SFC checked, ${failed} failed`);
process.exit(failed ? 1 : 0);
