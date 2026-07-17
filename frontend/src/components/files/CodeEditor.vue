<script setup>
import { onBeforeUnmount, onMounted, ref, watch } from "vue";
import { basicSetup, EditorView } from "codemirror";
import { StreamLanguage } from "@codemirror/language";
import { Compartment, EditorState, Prec } from "@codemirror/state";
import { keymap } from "@codemirror/view";

const props = defineProps({
  modelValue: { type: String, default: "" },
  disabled: { type: Boolean, default: false },
  filePath: { type: String, default: "" },
});
const emit = defineEmits(["update:modelValue", "save"]);
const host = ref(null);
let editor;
let applyingExternal = false;
let languageRequest = 0;
const editable = new Compartment();
const syntax = new Compartment();

onMounted(() => {
  editor = new EditorView({
    parent: host.value,
    state: EditorState.create({
      doc: props.modelValue,
      extensions: [
        basicSetup,
        editable.of(EditorView.editable.of(!props.disabled)),
        syntax.of([]),
        Prec.high(keymap.of([{
          key: "Mod-s",
          preventDefault: true,
          run: () => {
            emit("save");
            return true;
          },
        }])),
        EditorView.lineWrapping,
        EditorView.updateListener.of((update) => {
          if (update.docChanged && !applyingExternal) emit("update:modelValue", update.state.doc.toString());
        }),
      ],
    }),
  });
  configureLanguage(props.filePath);
});

watch(() => props.modelValue, (value) => {
  if (!editor || value === editor.state.doc.toString()) return;
  applyingExternal = true;
  editor.dispatch({ changes: { from: 0, to: editor.state.doc.length, insert: value } });
  applyingExternal = false;
});

watch(() => props.disabled, (value) => {
  if (editor) editor.dispatch({ effects: editable.reconfigure(EditorView.editable.of(!value)) });
});

watch(() => props.filePath, configureLanguage);

onBeforeUnmount(() => {
  languageRequest += 1;
  editor?.destroy();
  editor = null;
});

async function configureLanguage(filePath) {
  const request = ++languageRequest;
  let extension = [];
  try {
    extension = await languageFor(filePath);
  } catch {
    extension = [];
  }
  if (editor && request === languageRequest) {
    editor.dispatch({ effects: syntax.reconfigure(extension) });
  }
}

async function languageFor(filePath) {
  const name = filePath.split("/").pop()?.toLowerCase() || "";
  const extension = name.includes(".") ? name.split(".").pop() : "";
  switch (extension) {
    case "json":
      return (await import("@codemirror/lang-json")).json();
    case "js":
    case "cjs":
    case "mjs":
    case "jsx":
      return (await import("@codemirror/lang-javascript")).javascript({ jsx: extension === "jsx" });
    case "ts":
    case "tsx":
      return (await import("@codemirror/lang-javascript")).javascript({
        typescript: true,
        jsx: extension === "tsx",
      });
    case "java":
      return (await import("@codemirror/lang-java")).java();
    case "md":
    case "markdown":
      return (await import("@codemirror/lang-markdown")).markdown();
    case "xml":
      return (await import("@codemirror/lang-xml")).xml();
    case "yml":
    case "yaml":
      return (await import("@codemirror/lang-yaml")).yaml();
    case "go":
      return StreamLanguage.define((await import("@codemirror/legacy-modes/mode/go")).go);
    case "sh":
    case "bash":
    case "zsh":
      return StreamLanguage.define((await import("@codemirror/legacy-modes/mode/shell")).shell);
    case "toml":
      return StreamLanguage.define((await import("@codemirror/legacy-modes/mode/toml")).toml);
    case "properties":
    case "ini":
    case "conf":
    case "cfg":
      return StreamLanguage.define((await import("@codemirror/legacy-modes/mode/properties")).properties);
    default:
      if (name === ".env" || name.startsWith(".env.")) {
        return StreamLanguage.define((await import("@codemirror/legacy-modes/mode/properties")).properties);
      }
      return [];
  }
}
</script>

<template><div ref="host" class="code-editor" /></template>

<style scoped>
.code-editor { min-height: 420px; border: 1px solid #ccd4ce; background: #fff; }
.code-editor :deep(.cm-editor) { min-height: 420px; font-size: 13px; }
.code-editor :deep(.cm-scroller) { min-height: 420px; font-family: "Cascadia Code", "SFMono-Regular", Consolas, monospace; }
.code-editor :deep(.cm-focused) { outline: 2px solid rgba(45, 123, 87, 0.16); outline-offset: -1px; }
</style>
