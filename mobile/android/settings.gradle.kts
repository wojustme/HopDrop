pluginManagement {
    repositories {
        google()
        mavenCentral()
        gradlePluginPortal()
    }
}
dependencyResolutionManagement {
    repositoriesMode.set(RepositoriesMode.PREFER_SETTINGS)
    repositories {
        google()
        mavenCentral()
        // gomobile 生成的 hopdrop.aar 通过 app/libs 的 flatDir 引入。
        flatDir { dirs("app/libs") }
    }
}
rootProject.name = "HopDrop"
include(":app")
