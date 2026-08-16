import ReactDiffViewer, { DiffMethod } from 'react-diff-viewer-continued';
import type { ToolActivity } from '@/protocol.gen';
import type { ToolArgs } from './types';
import { number, text } from './utils';
import { languageForPath } from './fileLanguage';

type EditPreview = { oldText?: unknown; newText?: unknown; removedLines?: unknown; addedLines?: unknown };

const diffHighlightTheme = {
  default: 'var(--fg)',
  comment: 'var(--fg-faint)', prolog: 'var(--fg-faint)', doctype: 'var(--fg-faint)', cdata: 'var(--fg-faint)',
  punctuation: 'var(--fg-dim)', operator: 'var(--fg-dim)',
  keyword: 'var(--primary)', boolean: 'var(--primary)', important: 'var(--primary)',
  function: 'color-mix(in oklch, var(--primary) 78%, var(--fg))', 'class-name': 'color-mix(in oklch, var(--primary) 78%, var(--fg))',
  string: 'var(--ok)', char: 'var(--ok)', regex: 'var(--ok)', 'attr-value': 'var(--ok)',
  number: 'var(--warn)', constant: 'var(--warn)', symbol: 'var(--warn)',
  property: 'color-mix(in oklch, var(--primary) 65%, var(--fg))', tag: 'color-mix(in oklch, var(--primary) 65%, var(--fg))',
  variable: 'var(--fg)', builtin: 'var(--fg)', inserted: 'var(--ok)', deleted: 'var(--danger)',
};

const diffStyles = {
  variables: {
    light: {
      diffViewerBackground: 'var(--bg)', diffViewerColor: 'var(--fg)',
      diffViewerTitleBackground: 'var(--bg-alt)', diffViewerTitleColor: 'var(--fg-dim)',
      diffViewerTitleBorderColor: 'var(--rule)', gutterBackground: 'var(--bg-alt)',
      gutterColor: 'var(--fg-faint)', addedBackground: 'color-mix(in srgb, var(--ok) 10%, var(--bg))',
      removedBackground: 'color-mix(in srgb, var(--danger) 10%, var(--bg))',
      addedGutterBackground: 'color-mix(in srgb, var(--ok) 18%, var(--bg))',
      removedGutterBackground: 'color-mix(in srgb, var(--danger) 18%, var(--bg))',
      wordAddedBackground: 'color-mix(in srgb, var(--ok) 28%, transparent)',
      wordRemovedBackground: 'color-mix(in srgb, var(--danger) 28%, transparent)',
      codeFoldBackground: 'var(--bg-alt)', codeFoldGutterBackground: 'var(--bg-alt)',
      codeFoldContentColor: 'var(--fg-faint)', emptyLineBackground: 'var(--bg-alt)',
    },
  },
  diffContainer: { fontFamily: 'var(--mono)', fontSize: '11px', lineHeight: 1.55 },
  contentText: { fontFamily: 'inherit', lineHeight: 'inherit' },
  lineNumber: { fontFamily: 'inherit', lineHeight: 'inherit' },
  gutter: { minWidth: '38px', padding: '0 8px', borderColor: 'var(--rule)' },
  lineContent: { width: 'calc(50% - 38px)' },
  titleBlock: { padding: '6px 10px', borderColor: 'var(--rule)', fontSize: '10px', textTransform: 'uppercase' as const, letterSpacing: '.05em' },
};

export function EditFileBody({ tool, args }: { tool: ToolActivity; args: ToolArgs }) {
  const edits = Array.isArray(args.edits) ? args.edits as EditPreview[] : [];
  const language = languageForPath(text(args, 'path'));

  return (
    <div className="edit-output">
      {edits.map((edit, index) => {
        const oldText = typeof edit.oldText === 'string' ? edit.oldText : '';
        const newText = typeof edit.newText === 'string' ? edit.newText : '';
        return (
          <section className="edit-block" key={index}>
            <header>Change {index + 1}<span>−{String(edit.removedLines ?? 0)} +{String(edit.addedLines ?? 0)} lines</span></header>
            <div className="edit-diff" aria-label={`Split diff for change ${index + 1}`}>
              <ReactDiffViewer
                oldValue={oldText}
                newValue={newText}
                splitView
                leftTitle="Before"
                rightTitle="After"
                compareMethod={DiffMethod.WORDS_WITH_SPACE}
                showDiffOnly={false}
                hideSummary
                disableWorker
                highlightLanguage={language}
                highlightTheme={diffHighlightTheme}
                styles={diffStyles}
              />
            </div>
          </section>
        );
      })}
      {number(args, 'editsTruncated') ? <p className="tool-note">{number(args, 'editsTruncated')} more changes not shown.</p> : null}
      {tool.preview && !edits.length ? <div className="edit-status">{tool.preview}</div> : null}
      {!edits.length && !tool.preview ? <p className="tool-empty">Waiting for result…</p> : null}
    </div>
  );
}
