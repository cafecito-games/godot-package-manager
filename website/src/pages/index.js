import Heading from '@theme/Heading';
import Link from '@docusaurus/Link';
import Layout from '@theme/Layout';
import styles from './index.module.css';

const sections = [
  {
    num: '01',
    tag: 'manifest',
    title: 'Declare addons',
    to: '/docs/manifest',
    detail: 'Track Git, GitHub release, and archive addons in addons.toml.',
  },
  {
    num: '02',
    tag: 'install',
    title: 'Install repeatably',
    to: '/docs/lockfile',
    detail: 'Use addons.lock to keep machines and CI on the same pins.',
  },
  {
    num: '03',
    tag: 'private',
    title: 'Fetch private sources',
    to: '/docs/authentication',
    detail: 'Use existing git credentials or a GitHub token for releases.',
  },
];

const stats = [
  {label: 'TARGET', value: 'Godot addons'},
  {label: 'MANIFEST', value: 'addons.toml'},
  {label: 'LOCKFILE', value: 'addons.lock'},
  {label: 'SOURCES', value: 'git / release / archive'},
];

export default function Home() {
  return (
    <Layout
      title="Godot Package Manager"
      description="gpm - manifest-driven addon management for Godot projects">
      <main className={styles.main}>
        <div className={styles.gridOverlay} aria-hidden />

        <section className={styles.hero}>
          <div className={styles.heroContainer}>
            <div className={styles.statusBar}>
              <span className={styles.statusItem}>
                <span className={styles.pulseDot} />
                MANIFEST READY
              </span>
              <span className={styles.statusItem}>REPRODUCIBLE INSTALLS</span>
              <span className={styles.statusItem}>CI FRIENDLY</span>
            </div>

            <div className={styles.heroBody}>
              <Heading as="h1" className={styles.title}>
                <span className={styles.titleLine}>
                  <span className={styles.titleBracket}>[</span>
                  gpm
                  <span className={styles.titleBracket}>]</span>
                </span>
                <span className={styles.titleSub}>
                  godot<span className={styles.slash}>/</span>
                  <span className={styles.accent}>package</span> manager
                </span>
              </Heading>

              <p className={styles.lede}>
                Declare every Godot addon your project depends on, install
                them into addons/, and commit exact pins so teammates and CI
                get the same project every time.
              </p>

              <div className={styles.heroActions}>
                <Link className="button button--primary" to="/docs/quickstart">
                  <span className={styles.btnContent}>
                    <span>Quickstart</span>
                    <span className={styles.btnArrow}>→</span>
                  </span>
                </Link>
                <Link className="button button--secondary" to="/docs/manifest">
                  <span className={styles.btnContent}>
                    <span>Read manifest docs</span>
                  </span>
                </Link>
              </div>
            </div>

            <div className={styles.heroAside}>
              <div className={styles.terminal}>
                <div className={styles.terminalBar}>
                  <div className={styles.terminalDots}>
                    <span />
                    <span />
                    <span />
                  </div>
                  <span className={styles.terminalTitle}>~/game · zsh</span>
                  <span className={styles.terminalBadge}>LIVE</span>
                </div>
                <pre className={styles.terminalBody}>
                  <code>
                    <span className={styles.line}>
                      <span className={styles.lineNo}>01</span>
                      <span className={styles.prompt}>$</span>{' '}
                      <span className={styles.cmd}>gpm</span> init
                    </span>
                    <span className={styles.lineCmt}>
                      <span className={styles.lineNo}>02</span>
                      → addons.toml
                    </span>
                    <span className={styles.line}>
                      <span className={styles.lineNo}>03</span>
                      <span className={styles.prompt}>$</span>{' '}
                      <span className={styles.cmd}>gpm</span> add
                    </span>
                    <span className={styles.lineCmt}>
                      <span className={styles.lineNo}>04</span>
                      → addons/dialogue_manager
                    </span>
                    <span className={styles.line}>
                      <span className={styles.lineNo}>05</span>
                      <span className={styles.prompt}>$</span>{' '}
                      <span className={styles.cmd}>git</span> add{' '}
                      <span className={styles.flag}>addons.toml</span>{' '}
                      <span className={styles.flag}>addons.lock</span>
                    </span>
                    <span className={styles.line}>
                      <span className={styles.lineNo}>06</span>
                      <span className={styles.cursor}>▍</span>
                    </span>
                  </code>
                </pre>
              </div>
            </div>
          </div>

          <dl className={styles.stats}>
            {stats.map((s) => (
              <div key={s.label} className={styles.stat}>
                <dt>{s.label}</dt>
                <dd>{s.value}</dd>
              </div>
            ))}
          </dl>
        </section>

        <section className={styles.docsSection}>
          <div className={styles.docsHead}>
            <span className={styles.kicker}>
              <span className={styles.kickerBar} />
              SECTIONS / 03
            </span>
            <h2 className={styles.docsTitle}>
              Three paths{' '}
              <span className={styles.accent}>through the package flow.</span>
            </h2>
          </div>

          <div className={styles.cardGrid}>
            {sections.map((s) => (
              <Link to={s.to} className={styles.card} key={s.to}>
                <div className={styles.cardTop}>
                  <span className={styles.cardNum}>{s.num}</span>
                  <span className={styles.cardTag}>{s.tag}</span>
                </div>
                <div className={styles.cardBody}>
                  <h3 className={styles.cardTitle}>{s.title}</h3>
                  <p className={styles.cardDetail}>{s.detail}</p>
                </div>
                <div className={styles.cardArrow}>
                  <span>OPEN</span>
                  <span className={styles.cardArrowIcon}>→</span>
                </div>
                <span className={styles.cardCorner} aria-hidden />
              </Link>
            ))}
          </div>
        </section>

        <section className={styles.colophon}>
          <div className={styles.colophonContent}>
            <span className={styles.kicker}>
              <span className={styles.kickerBar} />
              ABOUT
            </span>
            <p>
              Built and maintained by{' '}
              <a href="https://www.cafecito.games/">Cafecito Games</a>{' '}
              for Godot teams that want addon installs to be explicit,
              reviewable, and reproducible. Open source.
            </p>
          </div>
        </section>
      </main>
    </Layout>
  );
}
