# Add project specific ProGuard rules here.
# Keep gomobile-generated classes
-keep class go.** { *; }
-keep class mobile.** { *; }

# Keep @JavascriptInterface methods so the WebView JS bridge works after minification
-keepclassmembers class * {
    @android.webkit.JavascriptInterface <methods>;
}
