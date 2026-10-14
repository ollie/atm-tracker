import { existsSync, readFileSync } from 'node:fs';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { resolve as resolvePath, dirname } from 'node:path';
import { stripTypeScriptTypes } from 'node:module';

const root = new URL('../../../../../node-simconnect/src/', import.meta.url).pathname;

export async function resolve(specifier, context, next) {
  if (specifier === 'debug') {
    return { url: 'data:text/javascript,export default () => () => {};', shortCircuit: true };
  }
  if (specifier === 'ini') {
    return { url: 'data:text/javascript,export const parse = () => ({}); export default { parse };', shortCircuit: true };
  }
  if (specifier.startsWith('.') && !specifier.endsWith('.ts') && context.parentURL?.startsWith('file:')) {
    const base = resolvePath(dirname(fileURLToPath(context.parentURL)), specifier);
    for (const candidate of [base + '.ts', base + '/index.ts']) {
      if (existsSync(candidate)) {
        return { url: pathToFileURL(candidate).href, shortCircuit: true };
      }
    }
  }
  return next(specifier, context);
}

function stripComments(s) {
  return s.replace(/\/\*[\s\S]*?\*\//g, '').replace(/\/\/.*$/gm, '');
}

function enumToObject(exported, name, body) {
  const members = stripComments(body).split(',').map(m => m.trim()).filter(Boolean);
  let lets = '', prev = null;
  const names = [];
  for (const m of members) {
    const eq = m.indexOf('=');
    const key = (eq < 0 ? m : m.slice(0, eq)).trim();
    const value = eq < 0 ? (prev === null ? '0' : `${prev} + 1`) : m.slice(eq + 1).trim().replaceAll(name + '.', '');
    lets += `let ${key} = ${value};\n`;
    names.push(key);
    prev = key;
  }
  const pairs = names.map(n => `${n}, [${n}]: '${n}'`).join(', ');
  return `${exported ? 'export ' : ''}const ${name} = (() => {\n${lets}return { ${pairs} };\n})();`;
}

function namespaceToObject(name, body) {
  const names = [...body.matchAll(/export const (\w+)/g)].map(m => m[1]);
  return `export const ${name} = (() => {\n${body.replace(/export const /g, 'const ')}\nreturn { ${names.join(', ')} };\n})();`;
}

export async function load(url, context, next) {
  if (!url.startsWith(pathToFileURL(root).href)) return next(url, context);
  let source = readFileSync(fileURLToPath(url), 'utf8');
  source = source.replace(/^import \w+ = NodeJS\.\w+;$/m, '');
  let n = 0;
  source = source.replace(/^import \{([^}]*)\} from '(\.[^']*)';/gm, (_, names, from) => {
    const values = names.split(',').map(x => x.trim()).filter(x => x && !x.startsWith('type ')).map(x => x.replace(/ as /, ': '));
    if (values.length === 0) return '';
    const alias = `__m${n++}`;
    return `import * as ${alias} from '${from}';\nconst { ${values.join(', ')} } = ${alias};`;
  });
  source = source.replace(/^(export )?(?:const )?enum (\w+) \{([\s\S]*?)^\}/gm, (_, exported, name, body) => enumToObject(!!exported, name, body));
  source = source.replace(/^export namespace (\w+) \{([\s\S]*?)^\}/gm, (_, name, body) => namespaceToObject(name, body));
  return { format: 'module', source: stripTypeScriptTypes(source), shortCircuit: true };
}
