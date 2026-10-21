package de.raindancer118.naknak;

import android.Manifest;
import android.annotation.SuppressLint;
import android.app.DownloadManager;
import android.content.ActivityNotFoundException;
import android.content.BroadcastReceiver;
import android.content.Context;
import android.content.Intent;
import android.content.IntentFilter;
import android.content.pm.PackageManager;
import android.graphics.Bitmap;
import android.net.Uri;
import android.os.Build;
import android.os.Bundle;
import android.os.Environment;
import android.os.Message;
import android.os.SystemClock;
import android.view.View;
import android.view.WindowManager;
import android.webkit.CookieManager;
import android.webkit.RenderProcessGoneDetail;
import android.webkit.URLUtil;
import android.webkit.ValueCallback;
import android.webkit.WebChromeClient;
import android.webkit.WebResourceError;
import android.webkit.WebResourceRequest;
import android.webkit.WebSettings;
import android.webkit.WebView;
import android.webkit.WebViewClient;
import android.widget.Toast;
import androidx.activity.OnBackPressedCallback;
import androidx.activity.result.ActivityResultLauncher;
import androidx.activity.result.contract.ActivityResultContracts;
import androidx.appcompat.app.AppCompatActivity;
import androidx.biometric.BiometricManager;
import androidx.biometric.BiometricPrompt;
import androidx.browser.customtabs.CustomTabsIntent;
import androidx.core.content.ContextCompat;
import androidx.core.splashscreen.SplashScreen;
import androidx.swiperefreshlayout.widget.SwipeRefreshLayout;
import androidx.webkit.WebSettingsCompat;
import androidx.webkit.WebViewFeature;
import java.util.HashSet;
import java.util.Set;

public class MainActivity extends AppCompatActivity {
    static final String EXTRA_PATH = "path";
    private static final long RELOCK_AFTER_MS = 30_000;

    private WebView web;
    private SwipeRefreshLayout swipe;
    private View offlineView;
    private View cover;
    private String server;
    private LinkPolicy policy;
    private ValueCallback<Uri[]> pendingUpload;
    private ActivityResultLauncher<Intent> uploadPicker;
    private final Set<Long> ownDownloads = new HashSet<>();
    private long backgroundSince;
    private boolean unlocked;

    private final BroadcastReceiver downloadDone = new BroadcastReceiver() {
        @Override
        public void onReceive(Context c, Intent i) {
            long id = i.getLongExtra(DownloadManager.EXTRA_DOWNLOAD_ID, -1);
            if (!ownDownloads.remove(id)) return;
            DownloadManager dm = getSystemService(DownloadManager.class);
            Uri uri = dm.getUriForDownloadedFile(id);
            if (uri == null) return;
            Intent view = new Intent(Intent.ACTION_VIEW).setDataAndType(uri, dm.getMimeTypeForDownloadedFile(id))
                .addFlags(Intent.FLAG_GRANT_READ_URI_PERMISSION | Intent.FLAG_ACTIVITY_NEW_TASK);
            try {
                startActivity(view);
            } catch (ActivityNotFoundException e) {
                Toast.makeText(c, R.string.download_saved, Toast.LENGTH_LONG).show();
            }
        }
    };

    @SuppressLint("SetJavaScriptEnabled")
    @Override
    protected void onCreate(Bundle state) {
        SplashScreen.installSplashScreen(this);
        super.onCreate(state);
        server = Prefs.server(this);
        if (server == null) {
            startActivity(new Intent(this, SetupActivity.class));
            finish();
            return;
        }
        policy = new LinkPolicy(server);
        setContentView(R.layout.activity_main);
        web = findViewById(R.id.web);
        swipe = findViewById(R.id.swipe);
        cover = findViewById(R.id.cover);
        offlineView = findViewById(R.id.offline);
        findViewById(R.id.retry).setOnClickListener(v -> {
            offlineView.setVisibility(View.GONE);
            web.reload();
        });
        findViewById(R.id.unlock).setOnClickListener(v -> unlock());

        uploadPicker = registerForActivityResult(new ActivityResultContracts.StartActivityForResult(), r -> {
            if (pendingUpload == null) return;
            pendingUpload.onReceiveValue(WebChromeClient.FileChooserParams.parseResult(r.getResultCode(), r.getData()));
            pendingUpload = null;
        });

        WebSettings s = web.getSettings();
        s.setJavaScriptEnabled(true);
        s.setDomStorageEnabled(true);
        s.setSupportMultipleWindows(true); // window.open() → onCreateWindow
        s.setAllowFileAccess(false);
        s.setAllowContentAccess(false);
        s.setUserAgentString(s.getUserAgentString() + " naknak-android/" + BuildConfig.VERSION_NAME);
        // WebAuthn is off in a WebView by default: Authentik's passkey login
        // then fails with "Error creating credential". FOR_APP goes through
        // Credential Manager, which only hands out a passkey for an RP ID
        // whose /.well-known/assetlinks.json names this app (portal.tstieh.de).
        if (WebViewFeature.isFeatureSupported(WebViewFeature.WEB_AUTHENTICATION)) {
            WebSettingsCompat.setWebAuthenticationSupport(s, WebSettingsCompat.WEB_AUTHENTICATION_SUPPORT_FOR_APP);
        }
        CookieManager.getInstance().setAcceptCookie(true);

        web.setWebViewClient(new WebViewClient() {
            @Override
            public boolean shouldOverrideUrlLoading(WebView v, WebResourceRequest r) {
                String url = r.getUrl().toString();
                if (policy.inApp(url, r.isRedirect(), v.getUrl())) return false;
                openOutside(url);
                return true;
            }

            @Override
            public void onPageStarted(WebView v, String url, Bitmap icon) {
                swipe.setRefreshing(false);
            }

            // the page itself failed (not a sub-resource): only then the native screen
            @Override
            public void onReceivedError(WebView v, WebResourceRequest r, WebResourceError e) {
                if (r.isForMainFrame()) offlineView.setVisibility(View.VISIBLE);
            }

            @Override
            public void onPageFinished(WebView v, String url) {
                swipe.setRefreshing(false);
                CookieManager.getInstance().flush();
            }

            // the renderer died (crash or out of memory): start over instead
            // of taking the whole app down with it
            @Override
            public boolean onRenderProcessGone(WebView v, RenderProcessGoneDetail d) {
                web = null;
                recreate();
                return true;
            }
        });
        web.setWebChromeClient(new WebChromeClient() {
            @Override
            public boolean onShowFileChooser(WebView v, ValueCallback<Uri[]> cb, FileChooserParams p) {
                if (pendingUpload != null) pendingUpload.onReceiveValue(null);
                pendingUpload = cb;
                try {
                    uploadPicker.launch(p.createIntent());
                } catch (ActivityNotFoundException e) {
                    pendingUpload = null;
                    return false;
                }
                return true;
            }

            // naknak opens files with window.open() and then sets the URL;
            // a throwaway WebView catches that URL and hands it over.
            @Override
            public boolean onCreateWindow(WebView v, boolean dialog, boolean user, Message msg) {
                WebView catcher = new WebView(MainActivity.this);
                catcher.setWebViewClient(new WebViewClient() {
                    @Override
                    public boolean shouldOverrideUrlLoading(WebView w, WebResourceRequest r) {
                        handleNewWindow(r.getUrl().toString());
                        w.destroy();
                        return true;
                    }

                    @Override
                    public boolean onRenderProcessGone(WebView w, RenderProcessGoneDetail d) {
                        w.destroy();
                        return true;
                    }
                });
                ((WebView.WebViewTransport) msg.obj).setWebView(catcher);
                msg.sendToTarget();
                return true;
            }
        });
        web.setDownloadListener((url, ua, disposition, mime, length) -> download(url, disposition, mime));
        AppBridge bridge = new AppBridge(this, server);
        web.addJavascriptInterface(bridge, "NaknakApp");
        swipe.setOnChildScrollUpCallback((parent, child) -> bridge.gestureHeld() || (child != null && child.canScrollVertically(-1)));

        swipe.setOnRefreshListener(() -> web.reload());
        swipe.setColorSchemeResources(R.color.cis, R.color.moodle);

        getOnBackPressedDispatcher().addCallback(this, new OnBackPressedCallback(true) {
            @Override
            public void handleOnBackPressed() {
                if (web.canGoBack()) web.goBack();
                else finish();
            }
        });

        ContextCompat.registerReceiver(this, downloadDone, new IntentFilter(DownloadManager.ACTION_DOWNLOAD_COMPLETE), ContextCompat.RECEIVER_EXPORTED);

        if (state != null) web.restoreState(state);
        else web.loadUrl(target(getIntent()));

        if (Build.VERSION.SDK_INT >= 33 && ContextCompat.checkSelfPermission(this, Manifest.permission.POST_NOTIFICATIONS) != PackageManager.PERMISSION_GRANTED) {
            registerForActivityResult(new ActivityResultContracts.RequestPermission(), granted -> {}).launch(Manifest.permission.POST_NOTIFICATIONS);
        }
        App.schedule(this);
    }

    private String target(Intent i) {
        String path = i == null ? null : i.getStringExtra(EXTRA_PATH);
        if (path == null || path.isEmpty()) return server + "/";
        if (path.startsWith("#")) return server + "/" + path;
        return path.startsWith("/") ? server + path : server + "/";
    }

    @Override
    protected void onNewIntent(Intent i) {
        super.onNewIntent(i);
        if (web != null && i.hasExtra(EXTRA_PATH)) web.loadUrl(target(i));
    }

    private void handleNewWindow(String url) {
        Uri u = Uri.parse(url);
        if (policy.inApp(url, false) && u.getPath() != null && u.getPath().startsWith("/files/")) {
            download(url, null, null);
        } else if (policy.inApp(url, false)) {
            web.loadUrl(url);
        } else {
            openOutside(url);
        }
    }

    private void openOutside(String url) {
        if (!policy.external(url)) return;
        Uri u = Uri.parse(url);
        try {
            if ("https".equals(u.getScheme()) || "http".equals(u.getScheme())) {
                new CustomTabsIntent.Builder().setShowTitle(true).build().launchUrl(this, u);
            } else {
                startActivity(new Intent(Intent.ACTION_VIEW, u));
            }
        } catch (ActivityNotFoundException e) {
            Toast.makeText(this, R.string.no_app, Toast.LENGTH_SHORT).show();
        }
    }

    // Files are fetched by the system download manager with naknak's cookies
    // and opened when done (PDFs in the PDF app of the phone).
    private void download(String url, String disposition, String mime) {
        if (!policy.inApp(url, false)) {
            openOutside(url);
            return;
        }
        String clean = url.replace("?inline=1", "");
        String name = URLUtil.guessFileName(clean, disposition, mime);
        DownloadManager.Request r = new DownloadManager.Request(Uri.parse(clean))
            .addRequestHeader("Cookie", CookieManager.getInstance().getCookie(clean))
            .addRequestHeader("User-Agent", web.getSettings().getUserAgentString())
            .setTitle(name)
            .setNotificationVisibility(DownloadManager.Request.VISIBILITY_VISIBLE_NOTIFY_COMPLETED)
            .setDestinationInExternalPublicDir(Environment.DIRECTORY_DOWNLOADS, "naknak/" + name);
        if (mime != null) r.setMimeType(mime);
        ownDownloads.add(getSystemService(DownloadManager.class).enqueue(r));
        Toast.makeText(this, getString(R.string.download_started, name), Toast.LENGTH_SHORT).show();
    }

    // ── app lock ────────────────────────────────────────────────────────────

    @Override
    protected void onStart() {
        super.onStart();
        WidgetWorker.now(this);
        if (web == null) return;
        boolean lock = Prefs.lock(this);
        // no screenshots / recents preview of grades while the lock is on
        if (lock) getWindow().addFlags(WindowManager.LayoutParams.FLAG_SECURE);
        else getWindow().clearFlags(WindowManager.LayoutParams.FLAG_SECURE);
        boolean expired = backgroundSince == 0 || SystemClock.elapsedRealtime() - backgroundSince > RELOCK_AFTER_MS;
        if (lock && (!unlocked || expired)) {
            unlocked = false;
            cover.setVisibility(View.VISIBLE);
            unlock();
        } else {
            cover.setVisibility(View.GONE);
        }
    }

    @Override
    protected void onStop() {
        super.onStop();
        backgroundSince = SystemClock.elapsedRealtime();
        CookieManager.getInstance().flush();
    }

    private void unlock() {
        int how = BiometricManager.Authenticators.BIOMETRIC_WEAK | BiometricManager.Authenticators.DEVICE_CREDENTIAL;
        if (BiometricManager.from(this).canAuthenticate(how) != BiometricManager.BIOMETRIC_SUCCESS) {
            // no lock screen on the phone: nothing to check against
            unlocked = true;
            cover.setVisibility(View.GONE);
            return;
        }
        new BiometricPrompt(this, ContextCompat.getMainExecutor(this), new BiometricPrompt.AuthenticationCallback() {
            @Override
            public void onAuthenticationSucceeded(BiometricPrompt.AuthenticationResult r) {
                unlocked = true;
                cover.setVisibility(View.GONE);
            }
        }).authenticate(new BiometricPrompt.PromptInfo.Builder()
            .setTitle(getString(R.string.lock_title))
            .setSubtitle(getString(R.string.lock_subtitle))
            .setAllowedAuthenticators(how)
            .build());
    }

    @Override
    protected void onSaveInstanceState(Bundle out) {
        super.onSaveInstanceState(out);
        if (web != null) web.saveState(out);
    }

    @Override
    protected void onDestroy() {
        if (policy != null) {
            try {
                unregisterReceiver(downloadDone);
            } catch (IllegalArgumentException ignored) {
            }
        }
        if (web != null) web.destroy();
        super.onDestroy();
    }
}
