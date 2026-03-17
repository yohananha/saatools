package com.saatool.app

import android.app.Activity
import android.content.ContentValues
import android.content.Intent
import android.net.Uri
import android.os.Build
import android.os.Bundle
import android.os.Environment
import android.provider.DocumentsContract
import android.provider.MediaStore
import android.util.Base64
import android.util.Log
import android.view.WindowManager
import android.webkit.JavascriptInterface
import android.webkit.ValueCallback
import android.webkit.WebChromeClient
import android.webkit.WebResourceRequest
import android.webkit.WebView
import android.webkit.WebViewClient
import androidx.appcompat.app.AlertDialog
import androidx.appcompat.app.AppCompatActivity
import androidx.core.view.WindowInsetsCompat
import androidx.core.view.WindowInsetsControllerCompat
import androidx.documentfile.provider.DocumentFile
import java.io.File

/**
 * MainActivity — hosts a full-screen WebView that loads the React frontend
 * from the local Go HTTP server at http://localhost:8766.
 *
 * WebChromeClient.onShowFileChooser is required to make <input type="file">
 * work inside a WebView — without it the file picker silently does nothing.
 */
class MainActivity : AppCompatActivity() {

    private lateinit var webView: WebView
    private var filePathCallback: ValueCallback<Array<Uri>>? = null
    private var pendingSafUri: Uri? = null

    override fun onCreate(savedInstanceState: Bundle?) {
        super.onCreate(savedInstanceState)

        // Hide the navigation bar — gives the WebView the full screen height.
        // Swiping up from the bottom edge reveals it temporarily.
        WindowInsetsControllerCompat(window, window.decorView).apply {
            hide(WindowInsetsCompat.Type.navigationBars())
            systemBarsBehavior =
                WindowInsetsControllerCompat.BEHAVIOR_SHOW_TRANSIENT_BARS_BY_SWIPE
        }

        // Start the Go server service before setting up the WebView.
        startForegroundService(Intent(this, GoServerService::class.java))

        // Enable Chrome DevTools remote debugging for debug builds only.
        // Connect via chrome://inspect on a desktop browser while USB-connected.
        val isDebuggable = (applicationInfo.flags and android.content.pm.ApplicationInfo.FLAG_DEBUGGABLE) != 0
        WebView.setWebContentsDebuggingEnabled(isDebuggable)

        webView = WebView(this).apply {
            settings.apply {
                javaScriptEnabled = true
                domStorageEnabled = true
                allowFileAccess = false
                allowContentAccess = false
                setSupportZoom(false)
                builtInZoomControls = false
                displayZoomControls = false
            }

            webViewClient = object : WebViewClient() {
                override fun shouldOverrideUrlLoading(
                    view: WebView,
                    request: WebResourceRequest
                ): Boolean {
                    val url = request.url.toString()
                    return !url.startsWith("http://localhost:${GoServerService.SERVER_PORT}")
                }
            }

            addJavascriptInterface(WebAppInterface(), "AndroidBridge")

            // Required to make <input type="file"> work in WebView.
            webChromeClient = object : WebChromeClient() {
                override fun onShowFileChooser(
                    webView: WebView,
                    callback: ValueCallback<Array<Uri>>,
                    params: FileChooserParams
                ): Boolean {
                    // Cancel any pending callback first.
                    filePathCallback?.onReceiveValue(null)
                    filePathCallback = callback

                    val intent = params.createIntent()
                    startActivityForResult(intent, FILE_CHOOSER_REQUEST)
                    return true
                }
            }
        }

        setContentView(webView)

        // Give the Go server ~1.5 s to bind to the port, then load the app.
        webView.postDelayed({
            Log.i(TAG, "Loading http://localhost:${GoServerService.SERVER_PORT}/")
            webView.loadUrl("http://localhost:${GoServerService.SERVER_PORT}/")
        }, 1500)
    }

    @Suppress("OVERRIDE_DEPRECATION")
    override fun onActivityResult(requestCode: Int, resultCode: Int, data: Intent?) {
        if (requestCode == FILE_CHOOSER_REQUEST) {
            val results = if (resultCode == Activity.RESULT_OK)
                WebChromeClient.FileChooserParams.parseResult(resultCode, data)
            else null
            filePathCallback?.onReceiveValue(results)
            filePathCallback = null
        } else if (requestCode == FOLDER_PICKER_REQUEST) {
            if (resultCode == Activity.RESULT_OK && data?.data != null) {
                val treeUri = data.data!!
                try {
                    contentResolver.takePersistableUriPermission(treeUri,
                        Intent.FLAG_GRANT_READ_URI_PERMISSION or Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
                } catch (_: SecurityException) { }
                val path = treeUriToPath(treeUri)
                if (path != null) {
                    val escaped = path.replace("\\", "\\\\").replace("'", "\\'")
                    webView.evaluateJavascript("typeof window.onFolderChosen === 'function' && window.onFolderChosen('$escaped');", null)
                } else {
                    // Folder path not directly accessible (scoped storage). Store SAF URI and
                    // use DocumentFile-based writing in saveFile() via the saf://pending/ prefix.
                    pendingSafUri = treeUri
                    webView.evaluateJavascript("typeof window.onFolderChosen === 'function' && window.onFolderChosen('saf://pending/');", null)
                }
            }
        }
        @Suppress("DEPRECATION")
        super.onActivityResult(requestCode, resultCode, data)
    }

    private fun treeUriToPath(uri: Uri): String? {
        if (uri.scheme == "file") return uri.path
        if (uri.scheme != "content") return null
        val docId = DocumentsContract.getTreeDocumentId(uri) ?: return null
        if (docId.startsWith("primary:")) {
            val rel = docId.removePrefix("primary:")
            val root = Environment.getExternalStorageDirectory().absolutePath
            val path = File(root, rel).absolutePath
            return if (File(path).exists() || File(path).mkdirs()) path else null
        }
        // Try secondary storage (e.g. SD card): document ID is often "volumeName:relativePath"
        val colon = docId.indexOf(':')
        if (colon > 0) {
            val volume = docId.substring(0, colon)
            val rel = docId.substring(colon + 1)
            val storageRoot = File("/storage", volume)
            if (storageRoot.exists()) {
                val path = File(storageRoot, rel).absolutePath
                return if (File(path).exists() || File(path).mkdirs()) path else null
            }
        }
        return null
    }

    @Suppress("OVERRIDE_DEPRECATION")
    override fun onBackPressed() {
        if (webView.canGoBack()) webView.goBack()
        else @Suppress("DEPRECATION") super.onBackPressed()
    }

    /**
     * Write bytes to public Downloads using MediaStore (Android 10+) or direct file write (Android 9-).
     * MediaStore requires no special permission on any Android version.
     */
    private fun saveToDownloads(fileName: String, bytes: ByteArray): String {
        if (Build.VERSION.SDK_INT >= Build.VERSION_CODES.Q) {
            return try {
                val values = ContentValues().apply {
                    put(MediaStore.Downloads.DISPLAY_NAME, fileName)
                    put(MediaStore.Downloads.MIME_TYPE, mimeTypeFor(fileName))
                    put(MediaStore.Downloads.RELATIVE_PATH, Environment.DIRECTORY_DOWNLOADS)
                }
                val uri = contentResolver.insert(MediaStore.Downloads.EXTERNAL_CONTENT_URI, values)
                    ?: return "Cannot create file in Downloads"
                contentResolver.openOutputStream(uri)?.use { it.write(bytes) }
                    ?: return "Cannot open output stream for Downloads"
                "ok"
            } catch (e: Exception) {
                Log.e(TAG, "saveToDownloads (MediaStore) failed: $fileName", e)
                e.message ?: "Write failed"
            }
        }
        // Android 9 and below: direct write is allowed
        return try {
            val dir = Environment.getExternalStoragePublicDirectory(Environment.DIRECTORY_DOWNLOADS)
            dir.mkdirs()
            File(dir, fileName).also { it.writeBytes(bytes) }
            "ok"
        } catch (e: Exception) {
            Log.e(TAG, "saveToDownloads (direct) failed: $fileName", e)
            e.message ?: "Write failed"
        }
    }

    private fun mimeTypeFor(fileName: String): String = when {
        fileName.endsWith(".epub", ignoreCase = true) -> "application/epub+zip"
        fileName.endsWith(".txt",  ignoreCase = true) -> "text/plain"
        else -> "application/octet-stream"
    }

    inner class WebAppInterface {
        @JavascriptInterface
        fun setKeepScreenOn(on: Boolean) {
            runOnUiThread {
                if (on) window.addFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
                else    window.clearFlags(WindowManager.LayoutParams.FLAG_KEEP_SCREEN_ON)
            }
        }

        /**
         * Shows a folder chooser dialog. "Downloads" uses a special marker so saveFile()
         * routes through MediaStore (no permission needed on any Android version).
         * "App storage" uses the app's external files dir. "Choose other…" opens the SAF picker.
         * Calls window.onFolderChosen(path).
         */
        @JavascriptInterface
        fun showFolderPresets() {
            runOnUiThread {
                val appPath = applicationContext.getExternalFilesDir(Environment.DIRECTORY_DOWNLOADS)?.absolutePath
                    ?: applicationContext.filesDir.absolutePath
                // "downloads://" is a virtual path — saveFile() routes it through MediaStore/direct write.
                val items = listOf(
                    "Downloads" to "downloads://",
                    "App storage" to appPath,
                    "Choose other\u2026" to null
                )
                val labels = items.map { it.first }.toTypedArray()
                AlertDialog.Builder(this@MainActivity)
                    .setTitle("Save to")
                    .setItems(labels) { _, which ->
                        val chosen = items.getOrNull(which) ?: return@setItems
                        if (chosen.second != null) {
                            val escaped = chosen.second!!.replace("\\", "\\\\").replace("'", "\\'")
                            webView.evaluateJavascript(
                                "typeof window.onFolderChosen === 'function' && window.onFolderChosen('$escaped');",
                                null)
                        } else {
                            val intent = Intent(Intent.ACTION_OPEN_DOCUMENT_TREE)
                            intent.addFlags(Intent.FLAG_GRANT_PERSISTABLE_URI_PERMISSION or
                                Intent.FLAG_GRANT_READ_URI_PERMISSION or
                                Intent.FLAG_GRANT_WRITE_URI_PERMISSION)
                            @Suppress("DEPRECATION")
                            startActivityForResult(intent, FOLDER_PICKER_REQUEST)
                        }
                    }
                    .setNegativeButton(android.R.string.cancel, null)
                    .show()
            }
        }

        /**
         * Writes base64-encoded content to a file. Returns "ok" or an error message.
         *
         * Special path prefixes:
         *   "downloads://"   — write to public Downloads via MediaStore (Android 10+) or direct (Android 9-)
         *   "saf://pending/" — write via SAF URI stored from the last folder picker
         */
        @JavascriptInterface
        fun saveFile(fullPath: String, base64Content: String): String {
            val bytes = try {
                Base64.decode(base64Content, Base64.DEFAULT)
            } catch (e: Exception) {
                return e.message ?: "Base64 decode failed"
            }

            if (fullPath.startsWith("downloads://")) {
                val fileName = fullPath.removePrefix("downloads://").trimStart('/')
                return saveToDownloads(fileName, bytes)
            }

            if (fullPath.startsWith("saf://pending/")) {
                val safUri = pendingSafUri
                    ?: return "No folder selected via storage picker"
                val fileName = fullPath.removePrefix("saf://pending/")
                return try {
                    val dir = DocumentFile.fromTreeUri(this@MainActivity, safUri)
                        ?: return "Cannot open selected folder"
                    dir.findFile(fileName)?.delete()
                    val newFile = dir.createFile(mimeTypeFor(fileName), fileName)
                        ?: return "Cannot create file in selected folder"
                    contentResolver.openOutputStream(newFile.uri)?.use { it.write(bytes) }
                        ?: return "Cannot write to selected folder"
                    "ok"
                } catch (e: Exception) {
                    Log.e(TAG, "saveFile (SAF) failed: $fullPath", e)
                    e.message ?: "Write failed"
                }
            }

            return try {
                val file = File(fullPath)
                file.parentFile?.mkdirs()
                file.writeBytes(bytes)
                "ok"
            } catch (e: Exception) {
                Log.e(TAG, "saveFile failed: $fullPath", e)
                e.message ?: "Write failed"
            }
        }
    }

    companion object {
        private const val TAG = "MainActivity"
        private const val FILE_CHOOSER_REQUEST = 1001
        private const val FOLDER_PICKER_REQUEST = 1002
    }
}
