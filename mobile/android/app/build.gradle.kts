plugins {
    id("com.android.application")
    id("org.jetbrains.kotlin.android")
}

android {
    namespace = "com.hopdrop.app"
    compileSdk = 34

    defaultConfig {
        applicationId = "com.hopdrop.app"
        // 接收落地用 MediaStore.Downloads（Android 10/API 29 起免运行时存储权限）。
        minSdk = 29
        targetSdk = 34
        versionCode = 1
        versionName = "0.1.0"
    }

    buildFeatures {
        compose = true
    }
    composeOptions {
        kotlinCompilerExtensionVersion = "1.5.14"
    }

    buildTypes {
        release {
            isMinifyEnabled = false
            // CI 无签名证书时用 debug 签名，保证 assembleRelease 能产出可安装 APK。
            signingConfig = signingConfigs.getByName("debug")
        }
    }
    compileOptions {
        sourceCompatibility = JavaVersion.VERSION_17
        targetCompatibility = JavaVersion.VERSION_17
    }
    kotlinOptions {
        jvmTarget = "17"
    }
    packaging {
        resources.excludes += "/META-INF/{AL2.0,LGPL2.1}"
    }
}

dependencies {
    // gomobile 产出的绑定库（scripts/build-android.sh / CI 会把 hopdrop.aar 放到 libs/）。
    // 经由 settings.gradle.kts 里配置的 flatDir 仓库解析，需显式给出空 group。
    implementation(group = "", name = "hopdrop", ext = "aar")

    implementation("androidx.core:core-ktx:1.13.1")
    implementation("androidx.activity:activity-compose:1.9.1")
    implementation(platform("androidx.compose:compose-bom:2024.06.00"))
    implementation("androidx.compose.ui:ui")
    implementation("androidx.compose.material3:material3")

    // 二维码：zxing core 负责生成，zxing-android-embedded 提供扫码 Activity。
    implementation("com.google.zxing:core:3.5.3")
    implementation("com.journeyapps:zxing-android-embedded:4.3.0")
}
